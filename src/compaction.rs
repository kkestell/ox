//! Selects complete transcript prefixes, summarizes them, and projects the
//! latest checkpoint into ordinary model requests.

use std::{
    collections::VecDeque,
    io::{self, ErrorKind},
};

use agent_client_protocol::schema::v1::SessionId;
use serde_json::Value;

use crate::{
    cancellation::PromptCancellation,
    openrouter::{self, Client},
    sessions::{
        CompactionCheckpoint, EffortLevel, HookFeedbackContent, SessionSettings, SessionStore,
        StopDecision, TranscriptEntry, UserMessagePart,
    },
};

const SUMMARY_OUTPUT_TOKENS: usize = 4096;
const SUMMARY_ALLOWANCE_BYTES: usize = SUMMARY_OUTPUT_TOKENS * 3;
const SUMMARY_LABEL: &str = "Compaction summary of earlier conversation:\n";
const TOOL_RESULT_EXCERPT_CHARS: usize = 2_000;
const IMAGE_ESTIMATE_TOKENS: usize = 4_096;

fn tokens(bytes: usize) -> usize {
    bytes.div_ceil(3)
}

/// Request-size limits for one model, in estimated tokens.
pub struct Budget {
    /// The largest request estimate Ox admits or sends.
    pub admission: usize,
    /// The request estimate at which a prompt run compacts before its next
    /// model request.
    pub automatic_threshold: usize,
    /// The request estimate compaction prefers a cut to reach.
    pub cut_target: usize,
}

pub fn budget(model: &str) -> io::Result<Budget> {
    let limit = openrouter::catalog_model(model)
        .ok_or_else(|| io::Error::new(ErrorKind::InvalidInput, "unknown model"))?
        .context_limit;
    let admission = limit - 8_000.max(limit / 10);
    Ok(Budget {
        admission,
        automatic_threshold: admission * 80 / 100,
        cut_target: admission * 60 / 100,
    })
}

pub fn request_estimate(
    model: &str,
    effort: EffortLevel,
    system: &str,
    transcript: &[TranscriptEntry],
) -> io::Result<usize> {
    let projected = projection(transcript);
    let body = openrouter::ordinary_body(model, effort, system, &projected)?;
    Ok(tokens(estimated_bytes(body)))
}

/// Count image data as a fixed token allowance. Encoded base64 is request
/// transport, not text for the model to tokenize.
fn estimated_bytes(mut body: Value) -> usize {
    let mut images = 0;
    if let Some(messages) = body.get_mut("messages").and_then(Value::as_array_mut) {
        for message in messages {
            images += strip_images(message);
        }
    }
    serde_json::to_vec(&body)
        .expect("request body serializes")
        .len()
        + images * IMAGE_ESTIMATE_TOKENS * 3
}

fn strip_images(message: &mut Value) -> usize {
    let mut images = 0;
    if let Some(parts) = message.get_mut("content").and_then(Value::as_array_mut) {
        for part in parts {
            if part.get("type").and_then(Value::as_str) == Some("image_url") {
                part["image_url"]["url"] = Value::String("[image]".to_owned());
                images += 1;
            }
        }
    }
    images
}

fn latest(transcript: &[TranscriptEntry]) -> Option<&CompactionCheckpoint> {
    transcript.iter().rev().find_map(|entry| match entry {
        TranscriptEntry::CompactionCheckpoint(checkpoint) => Some(checkpoint),
        _ => None,
    })
}

pub fn projection(transcript: &[TranscriptEntry]) -> Vec<TranscriptEntry> {
    match latest(transcript) {
        Some(checkpoint) => {
            projection_at(transcript, checkpoint.covered_prefix, &checkpoint.summary)
        }
        None => transcript
            .iter()
            .filter(|entry| !matches!(entry, TranscriptEntry::CompactionCheckpoint(_)))
            .cloned()
            .collect(),
    }
}

/// The index of the skill invocation to repeat after a summary covering
/// `[..cut]`: the latest turn start, when it is a covered skill invocation. A
/// long hook-driven run keeps its instructions and arguments this way until a
/// later turn begins.
fn repeated_invocation(transcript: &[TranscriptEntry], cut: usize) -> Option<usize> {
    let (index, entry) = transcript.iter().enumerate().rev().find(|(_, entry)| {
        matches!(
            entry,
            TranscriptEntry::UserMessage(_) | TranscriptEntry::SkillInvocation(_)
        )
    })?;
    (index < cut && matches!(entry, TranscriptEntry::SkillInvocation(_))).then_some(index)
}

pub fn has_candidate(transcript: &[TranscriptEntry]) -> bool {
    !candidates(transcript).is_empty()
}

pub fn context_error() -> io::Error {
    io::Error::new(
        ErrorKind::InvalidInput,
        "conversation exceeds the model context limit and cannot be compacted further",
    )
}

fn projection_at(
    transcript: &[TranscriptEntry],
    cut: usize,
    summary: &str,
) -> Vec<TranscriptEntry> {
    let mut projected = vec![TranscriptEntry::UserMessage(
        format!("{SUMMARY_LABEL}{summary}").into(),
    )];
    if let Some(index) = repeated_invocation(transcript, cut) {
        projected.push(transcript[index].clone());
    }
    projected.extend(
        transcript[cut..]
            .iter()
            .filter(|entry| !matches!(entry, TranscriptEntry::CompactionCheckpoint(_)))
            .cloned(),
    );
    projected
}

fn candidates(transcript: &[TranscriptEntry]) -> Vec<usize> {
    let start = latest(transcript).map_or(0, |checkpoint| checkpoint.covered_prefix);
    let mut result = Vec::new();
    let mut index = start;
    while index < transcript.len() {
        if let TranscriptEntry::AssistantMessage(message) = &transcript[index] {
            index += 1 + message.tool_calls.len();
            if index <= transcript.len() {
                result.push(index);
            }
        } else {
            index += 1;
        }
    }
    result
}

fn projected_estimate(
    model: &str,
    effort: EffortLevel,
    system: &str,
    transcript: &[TranscriptEntry],
    cut: usize,
    summary: &str,
) -> io::Result<usize> {
    let body = openrouter::ordinary_body(
        model,
        effort,
        system,
        &projection_at(transcript, cut, summary),
    )?;
    Ok(tokens(estimated_bytes(body)))
}

/// A prospective prompt is rejected only if even the largest complete cut,
/// with room for a new summary, cannot fit the admission budget.
pub fn input_fits(
    model: &str,
    effort: EffortLevel,
    system: &str,
    prospective: &[TranscriptEntry],
) -> io::Result<bool> {
    let admission = budget(model)?.admission;
    if request_estimate(model, effort, system, prospective)? <= admission {
        return Ok(true);
    }
    let Some(cut) = candidates(prospective).last().copied() else {
        return Ok(false);
    };
    projected_estimate(
        model,
        effort,
        system,
        prospective,
        cut,
        &"x".repeat(SUMMARY_ALLOWANCE_BYTES),
    )
    .map(|estimate| estimate <= admission)
}

fn ranked_cuts(
    model: &str,
    effort: EffortLevel,
    system: &str,
    transcript: &[TranscriptEntry],
    original: usize,
) -> io::Result<Vec<usize>> {
    let Budget {
        admission,
        cut_target,
        ..
    } = budget(model)?;
    let start = latest(transcript).map_or(0, |checkpoint| checkpoint.covered_prefix);
    let cuts = candidates(transcript);
    let summary = TranscriptEntry::UserMessage(
        format!("{SUMMARY_LABEL}{}", "x".repeat(SUMMARY_ALLOWANCE_BYTES)).into(),
    );
    let base = openrouter::ordinary_body(model, effort, system, std::slice::from_ref(&summary))?;
    let base_bytes = estimated_bytes(base);
    let mut suffix_bytes = vec![0; transcript.len() - start + 1];
    for index in (start..transcript.len()).rev() {
        suffix_bytes[index - start] =
            suffix_bytes[index - start + 1] + message_bytes(&transcript[index]);
    }
    // Every cut past the latest skill invocation repeats it after the summary.
    let repeated = repeated_invocation(transcript, transcript.len())
        .map(|index| (index, message_bytes(&transcript[index])));
    let mut ranked = Vec::new();
    for cut in cuts {
        let repeated_bytes = repeated
            .filter(|&(index, _)| index < cut)
            .map_or(0, |(_, bytes)| bytes);
        let estimate = tokens(base_bytes + suffix_bytes[cut - start] + repeated_bytes);
        if estimate > admission {
            continue;
        }
        let rank = (estimate >= original, estimate > cut_target, estimate);
        ranked.push((cut, rank));
    }
    ranked.sort_by_key(|(_, rank)| *rank);
    Ok(ranked.into_iter().map(|(cut, _)| cut).collect())
}

/// The serialized size an entry adds to a request body, counting the comma that
/// separates it from the previous message.
fn message_bytes(entry: &TranscriptEntry) -> usize {
    openrouter::chat_messages(std::slice::from_ref(entry))
        .into_iter()
        .map(|mut message| {
            let images = strip_images(&mut message);
            serde_json::to_vec(&message)
                .expect("chat message serializes")
                .len()
                + images * IMAGE_ESTIMATE_TOKENS * 3
                + 1
        })
        .sum()
}

fn tool_result_excerpt(text: &str) -> String {
    let chars: Vec<char> = text.chars().collect();
    if chars.len() <= TOOL_RESULT_EXCERPT_CHARS {
        return text.to_owned();
    }
    let edge = TOOL_RESULT_EXCERPT_CHARS / 2;
    let head: String = chars[..edge].iter().collect();
    let tail: String = chars[chars.len() - edge..].iter().collect();
    let omitted = chars.len() - TOOL_RESULT_EXCERPT_CHARS;
    format!("{head}\n[... {omitted} characters omitted from tool result ...]\n{tail}")
}

fn material(transcript: &[TranscriptEntry], cut: usize) -> VecDeque<(String, String, usize)> {
    let start = latest(transcript).map_or(0, |checkpoint| checkpoint.covered_prefix);
    let mut fields = VecDeque::new();
    for (index, entry) in transcript[start..cut].iter().enumerate() {
        let source = format!("Entry {}", start + index);
        match entry {
            TranscriptEntry::UserMessage(message) => {
                for part in &message.parts {
                    let value = match part {
                        UserMessagePart::Text(text) => text.clone(),
                        UserMessagePart::Image(image) => format!("[image: {}]", image.mime_type),
                    };
                    fields.push_back((format!("{source} user request"), value, 1));
                }
            }
            TranscriptEntry::SkillInvocation(invocation) => {
                fields.push_back((
                    format!("{source} user request"),
                    openrouter::skill_invocation_text(invocation),
                    1,
                ));
                for image in &invocation.images {
                    fields.push_back((
                        format!("{source} user request"),
                        format!("[image: {}]", image.mime_type),
                        1,
                    ));
                }
            }
            TranscriptEntry::HookFeedback(feedback) => {
                let decision = match feedback.content {
                    HookFeedbackContent::BeforeStop {
                        decision: StopDecision::Continue,
                        ..
                    } => " continue",
                    HookFeedbackContent::BeforeStop {
                        decision: StopDecision::Stop,
                        ..
                    } => " stop",
                    HookFeedbackContent::BeforeRun { .. }
                    | HookFeedbackContent::AfterTools { .. } => "",
                };
                fields.push_back((
                    format!("{source} {}{decision} feedback", feedback.label()),
                    feedback.message().to_owned(),
                    1,
                ))
            }
            TranscriptEntry::AssistantMessage(message) => {
                if !message.text.is_empty() {
                    fields.push_back((
                        format!("{source} assistant answer"),
                        message.text.clone(),
                        1,
                    ));
                }
                for call in &message.tool_calls {
                    if !call.arguments.is_empty() {
                        fields.push_back((
                            format!("{source} tool {} arguments", call.name),
                            call.arguments.clone(),
                            1,
                        ));
                    }
                }
            }
            TranscriptEntry::ToolResult(result) => {
                let status = match result.outcome {
                    crate::sessions::ToolOutcome::Completed(_) => "completed",
                    crate::sessions::ToolOutcome::Failed(_) => "failed",
                    crate::sessions::ToolOutcome::Cancelled(_) => "cancelled",
                };
                if !result.outcome.text().is_empty() {
                    fields.push_back((
                        format!("{source} tool {} {status} outcome", result.name),
                        tool_result_excerpt(result.outcome.text()),
                        1,
                    ));
                }
            }
            _ => {}
        }
    }
    fields
}

fn summary_request_fits(model: &str, previous: &str, piece: &str) -> io::Result<bool> {
    let limit = openrouter::catalog_model(model)
        .expect("validated model")
        .context_limit;
    let body = openrouter::summarizer_body(model, previous, piece)?;
    Ok(tokens(
        serde_json::to_vec(&body)
            .expect("summary body serializes")
            .len(),
    ) + SUMMARY_OUTPUT_TOKENS
        <= limit)
}

fn next_piece(
    model: &str,
    previous: &str,
    fields: &mut VecDeque<(String, String, usize)>,
) -> io::Result<String> {
    let mut piece = String::new();
    while let Some((label, value, part)) = fields.front() {
        let header = format!("{label}, part {part}:\n");
        let available = value.len();
        let mut take = available;
        loop {
            while !value.is_char_boundary(take) {
                take -= 1;
            }
            let addition = format!("{header}{}\n", &value[..take]);
            if summary_request_fits(model, previous, &(piece.clone() + &addition))? {
                break;
            }
            if take == 0 {
                break;
            }
            take /= 2;
        }
        if take == 0 {
            if piece.is_empty() {
                return Err(io::Error::new(
                    ErrorKind::InvalidInput,
                    "summarizer has no room for conversation material",
                ));
            }
            break;
        }
        piece.push_str(&header);
        piece.push_str(&value[..take]);
        piece.push('\n');
        if take == available {
            fields.pop_front();
        } else {
            let field = fields.front_mut().expect("field remains");
            field.1.drain(..take);
            field.2 += 1;
        }
    }
    Ok(piece)
}

pub async fn compact(
    store: &SessionStore,
    client: &Client,
    cancellation: &PromptCancellation,
    id: &SessionId,
    settings: &SessionSettings,
    system: &str,
    transcript: &mut Vec<TranscriptEntry>,
) -> io::Result<bool> {
    let model = settings.model.as_str();
    let effort = settings.effort;
    let original = request_estimate(model, effort, system, transcript)?;
    // Every summarizer request counts, including those for rejected cuts.
    let mut summarizer_cost: Option<f64> = None;
    for cut in ranked_cuts(model, effort, system, transcript, original)? {
        let mut fields = material(transcript, cut);
        if fields.is_empty() {
            continue;
        }
        let mut summary =
            latest(transcript).map_or(String::new(), |checkpoint| checkpoint.summary.clone());
        while !fields.is_empty() {
            let piece = next_piece(model, &summary, &mut fields)?;
            let (next, usage) = tokio::select! {
                biased;
                () = cancellation.cancelled() => return Err(io::Error::new(ErrorKind::Interrupted, "compaction cancelled")),
                result = client.summarize(model, &summary, &piece) => result?,
            };
            summary = next;
            if let Some(usage) = usage {
                *summarizer_cost.get_or_insert(0.0) += usage.cost;
            }
        }
        if cancellation.is_cancelled() {
            return Err(io::Error::new(
                ErrorKind::Interrupted,
                "compaction cancelled",
            ));
        }
        let actual = projected_estimate(model, effort, system, transcript, cut, &summary)?;
        if actual >= original || actual > budget(model)?.admission {
            continue;
        }
        let checkpoint = CompactionCheckpoint {
            summary,
            covered_prefix: cut,
            summarizer_cost,
        };
        store.append_checkpoint(id, transcript.len(), &checkpoint)?;
        transcript.push(TranscriptEntry::CompactionCheckpoint(checkpoint));
        return Ok(true);
    }
    Ok(false)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        openrouter::{
            default_model,
            fixture::{Reply, Server, text_reply},
        },
        sessions::{
            AssistantBatch, AssistantMessage, HookFeedback, SessionSettingsChange, SkillInvocation,
            ToolCall, ToolOutcome, ToolResult,
        },
    };

    fn answer(text: &str) -> AssistantBatch {
        AssistantBatch::new(
            AssistantMessage {
                text: text.to_owned(),
                reasoning: "private reasoning".to_owned(),
                tool_calls: vec![],
                continuation_metadata: vec![serde_json::json!({"opaque": true})],
                usage: None,
            },
            vec![],
        )
        .unwrap()
    }

    fn tool_batch(id: &str, output: &str) -> AssistantBatch {
        AssistantBatch::new(
            AssistantMessage {
                text: String::new(),
                reasoning: "private reasoning".to_owned(),
                tool_calls: vec![ToolCall {
                    call_id: id.to_owned(),
                    name: "shell".to_owned(),
                    arguments: "{\"command\":\"true\"}".to_owned(),
                }],
                continuation_metadata: vec![],
                usage: None,
            },
            vec![ToolResult {
                call_id: id.to_owned(),
                name: "shell".to_owned(),
                outcome: ToolOutcome::Completed(output.to_owned()),
            }],
        )
        .unwrap()
    }

    #[tokio::test]
    async fn manual_compaction_uses_a_summary_and_keeps_the_complete_transcript() {
        let store = SessionStore::in_memory();
        let id = store.create(std::path::Path::new("/workspace")).unwrap().id;
        store
            .append_turn_start(
                &id,
                &SessionSettingsChange {
                    model: Some(default_model().to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::SkillInvocation(SkillInvocation {
                    name: "goal".to_owned(),
                    arguments: "Record the old details.".to_owned(),
                    instructions: "Work until the hook stops you.".to_owned(),
                    images: vec![],
                }),
            )
            .unwrap();
        let feedback = |content| HookFeedback {
            skill: matches!(&content, HookFeedbackContent::BeforeStop { .. })
                .then(|| "goal".to_owned()),
            content,
        };
        store
            .append_hook_feedback(
                &id,
                &feedback(HookFeedbackContent::BeforeRun {
                    message: "The parser lives in src/parse.rs.".to_owned(),
                }),
            )
            .unwrap();
        store
            .append_batch(
                &id,
                &answer(&format!("Recorded {}", "old details ".repeat(3000))),
            )
            .unwrap();
        store
            .append_hook_feedback(
                &id,
                &feedback(HookFeedbackContent::BeforeStop {
                    decision: StopDecision::Stop,
                    message: "Objective met.".to_owned(),
                }),
            )
            .unwrap();
        let server = Server::start(vec![
            text_reply("Initial work complete."),
            text_reply("Active request carried."),
            text_reply("Active request carried; next tool complete."),
        ])
        .await;
        let cancellation = PromptCancellation::new();
        let mut transcript = store.read(&id).unwrap().unwrap().transcript;
        assert!(
            request_estimate(default_model(), EffortLevel::Default, "system", &transcript).unwrap()
                < budget(default_model()).unwrap().automatic_threshold,
            "manual compaction is below the automatic trigger"
        );
        assert!(
            compact(
                &store,
                &server.client(),
                &cancellation,
                &id,
                &SessionSettings::new(default_model(), EffortLevel::Default),
                "system",
                &mut transcript
            )
            .await
            .unwrap()
        );
        let first_cut = transcript.len() - 1;
        store
            .append_turn_start(
                &id,
                &SessionSettingsChange::default(),
                &TranscriptEntry::UserMessage("active request".to_owned().into()),
            )
            .unwrap();
        transcript.push(TranscriptEntry::UserMessage(
            "active request".to_owned().into(),
        ));
        let batch = tool_batch("first", &"new details ".repeat(3000));
        store.append_batch(&id, &batch).unwrap();
        transcript.push(TranscriptEntry::AssistantMessage(batch.message));
        transcript.extend(batch.results.into_iter().map(TranscriptEntry::ToolResult));
        assert!(
            compact(
                &store,
                &server.client(),
                &cancellation,
                &id,
                &SessionSettings::new(default_model(), EffortLevel::Default),
                "system",
                &mut transcript
            )
            .await
            .unwrap()
        );
        let batch = tool_batch("second", &"later details ".repeat(3000));
        store.append_batch(&id, &batch).unwrap();
        transcript.push(TranscriptEntry::AssistantMessage(batch.message));
        transcript.extend(batch.results.into_iter().map(TranscriptEntry::ToolResult));
        assert!(
            compact(
                &store,
                &server.client(),
                &cancellation,
                &id,
                &SessionSettings::new(default_model(), EffortLevel::Default),
                "system",
                &mut transcript
            )
            .await
            .unwrap()
        );
        assert_eq!(store.read(&id).unwrap().unwrap().transcript, transcript);
        let checkpoints = transcript
            .iter()
            .filter(|entry| matches!(entry, TranscriptEntry::CompactionCheckpoint(_)))
            .count();
        assert_eq!(checkpoints, 3);
        let projected = projection(&transcript);
        assert_eq!(projected, vec![TranscriptEntry::UserMessage(
            "Compaction summary of earlier conversation:\nActive request carried; next tool complete.".to_owned().into())]);
        let requests = server.requests();
        assert_eq!(requests.len(), 3);
        assert!(requests.iter().all(|request| request.get("tools").is_none()
            && request["reasoning"]["effort"] == "low"
            && request["max_tokens"] == 4096
            && request["usage"] == serde_json::json!({ "include": true })));
        let material = |index: usize| {
            requests[index]["messages"][1]["content"]
                .as_str()
                .unwrap()
                .to_owned()
        };
        assert!(material(0).contains("Entry 1 user request, part 1:\nSkill /goal invoked."));
        assert!(material(0).contains(
            "Entry 2 global before_run hook feedback, part 1:\nThe parser lives in src/parse.rs."
        ));
        assert!(material(1).contains(
            "Entry 4 skill /goal before_stop hook stop feedback, part 1:\nObjective met."
        ));
        assert!(
            requests[1]["messages"][1]["content"]
                .as_str()
                .unwrap()
                .contains("Initial work complete.")
        );
        assert!(
            requests[1]["messages"][1]["content"]
                .as_str()
                .unwrap()
                .contains("active request")
        );
        assert!(
            requests[2]["messages"][1]["content"]
                .as_str()
                .unwrap()
                .contains("Active request carried.")
        );
        assert!(
            requests[2]["messages"][1]["content"]
                .as_str()
                .unwrap()
                .contains("later details")
        );
        assert!(matches!(
            &transcript[first_cut],
            TranscriptEntry::CompactionCheckpoint(_)
        ));

        let image_message = crate::sessions::UserMessage {
            parts: vec![
                UserMessagePart::Text("Inspect this".to_owned()),
                UserMessagePart::Image(crate::sessions::ImageAttachment {
                    data: "aGVsbG8=".to_owned(),
                    mime_type: "image/png".to_owned(),
                }),
            ],
        };
        let mut image_transcript = vec![
            TranscriptEntry::Model(default_model().to_owned()),
            TranscriptEntry::UserMessage(image_message),
        ];
        let estimate = request_estimate(
            default_model(),
            EffortLevel::Default,
            "system",
            &image_transcript,
        )
        .unwrap();
        let fields = super::material(&image_transcript, 2);
        assert!(
            fields
                .iter()
                .any(|(_, value, _)| value == "[image: image/png]")
        );
        assert!(
            fields
                .iter()
                .all(|(_, value, _)| !value.contains("aGVsbG8="))
        );
        if let TranscriptEntry::UserMessage(message) = &mut image_transcript[1]
            && let UserMessagePart::Image(image) = &mut message.parts[1]
        {
            image.data = "A".repeat(4_000);
        }
        assert_eq!(
            estimate,
            request_estimate(
                default_model(),
                EffortLevel::Default,
                "system",
                &image_transcript
            )
            .unwrap()
        );
        assert!(
            matches!(&projection_at(&image_transcript, 1, "summary")[1], TranscriptEntry::UserMessage(message) if message.has_images())
        );

        let store = SessionStore::in_memory();
        let id = store.create(std::path::Path::new("/workspace")).unwrap().id;
        store
            .append_turn_start(
                &id,
                &SessionSettingsChange {
                    model: Some(default_model().to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("o".repeat(1_500_000).into()),
            )
            .unwrap();
        store.append_batch(&id, &answer("older work done")).unwrap();
        store
            .append_turn_start(
                &id,
                &SessionSettingsChange::default(),
                &TranscriptEntry::UserMessage("u".repeat(1_800_000).into()),
            )
            .unwrap();
        let mut transcript = store.read(&id).unwrap().unwrap().transcript;
        let server = Server::start(vec![text_reply("Older work summarized.")]).await;
        assert!(
            compact(
                &store,
                &server.client(),
                &PromptCancellation::new(),
                &id,
                &SessionSettings::new(default_model(), EffortLevel::Default),
                "system",
                &mut transcript
            )
            .await
            .unwrap()
        );
        let Budget {
            admission,
            cut_target,
            ..
        } = budget(default_model()).unwrap();
        let estimate =
            request_estimate(default_model(), EffortLevel::Default, "system", &transcript).unwrap();
        assert!(
            estimate > cut_target && estimate <= admission,
            "a useful reduction is accepted even when the target cannot be reached"
        );
    }

    #[tokio::test]
    async fn a_later_summary_failure_leaves_the_old_checkpoint_active() {
        let store = SessionStore::in_memory();
        let id = store.create(std::path::Path::new("/workspace")).unwrap().id;
        store
            .append_turn_start(
                &id,
                &SessionSettingsChange {
                    model: Some(default_model().to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("earlier work".to_owned().into()),
            )
            .unwrap();
        store.append_batch(&id, &answer("earlier answer")).unwrap();
        store
            .append_checkpoint(
                &id,
                3,
                &CompactionCheckpoint {
                    summary: "Earlier work is complete.".to_owned(),
                    covered_prefix: 3,
                    summarizer_cost: None,
                },
            )
            .unwrap();
        let large = "x".repeat(3_300_000);
        store
            .append_turn_start(
                &id,
                &SessionSettingsChange::default(),
                &TranscriptEntry::UserMessage(large.clone().into()),
            )
            .unwrap();
        store.append_batch(&id, &answer("done")).unwrap();
        let before = store.read(&id).unwrap().unwrap().transcript;
        let server = Server::start(vec![
            text_reply("provisional"),
            Reply::Status(500, "failed".to_owned()),
        ])
        .await;
        let mut transcript = before.clone();
        assert!(
            compact(
                &store,
                &server.client(),
                &PromptCancellation::new(),
                &id,
                &SessionSettings::new(default_model(), EffortLevel::Default),
                "system",
                &mut transcript
            )
            .await
            .is_err()
        );
        assert_eq!(
            server.requests().len(),
            2,
            "the oversized field spans two summary requests"
        );
        assert_eq!(transcript, before);
        assert_eq!(store.read(&id).unwrap().unwrap().transcript, before);

        let small_store = SessionStore::in_memory();
        let small_id = small_store
            .create(std::path::Path::new("/workspace"))
            .unwrap()
            .id;
        small_store
            .append_turn_start(
                &small_id,
                &SessionSettingsChange {
                    model: Some(default_model().to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("older ".repeat(4000).into()),
            )
            .unwrap();
        small_store
            .append_batch(&small_id, &answer("done"))
            .unwrap();
        let small_before = small_store.read(&small_id).unwrap().unwrap().transcript;
        for reply in [
            text_reply(""),
            Reply::Stream(crate::openrouter::fixture::sse(&[
                crate::openrouter::fixture::delta(
                    serde_json::json!({"role":"assistant", "content":"partial"}),
                    Some("length"),
                ),
            ])),
        ] {
            let server = Server::start(vec![reply]).await;
            let mut copy = small_before.clone();
            assert!(
                compact(
                    &small_store,
                    &server.client(),
                    &PromptCancellation::new(),
                    &small_id,
                    &SessionSettings::new(default_model(), EffortLevel::Default),
                    "system",
                    &mut copy
                )
                .await
                .is_err()
            );
            assert_eq!(copy, small_before);
            assert_eq!(
                small_store.read(&small_id).unwrap().unwrap().transcript,
                small_before
            );
        }
        small_store.with_connection(|connection| {
            connection
                .execute_batch(
                    "CREATE TRIGGER refuse_checkpoint BEFORE INSERT ON transcript_entries
             WHEN NEW.kind = 'compaction_checkpoint'
             BEGIN SELECT RAISE(ABORT, 'disk full'); END;",
                )
                .unwrap()
        });
        let server = Server::start(vec![text_reply("Older work done.")]).await;
        let mut copy = small_before.clone();
        assert!(
            compact(
                &small_store,
                &server.client(),
                &PromptCancellation::new(),
                &small_id,
                &SessionSettings::new(default_model(), EffortLevel::Default),
                "system",
                &mut copy
            )
            .await
            .is_err()
        );
        assert_eq!(copy, small_before);
        assert_eq!(
            small_store.read(&small_id).unwrap().unwrap().transcript,
            small_before
        );

        let hanging = Server::start(vec![Reply::Hang(": ping\n\n".to_owned())]).await;
        let cancel = PromptCancellation::new();
        let stop = cancel.clone();
        let mut copy = small_before.clone();
        let work = async {
            compact(
                &small_store,
                &hanging.client(),
                &cancel,
                &small_id,
                &SessionSettings::new(default_model(), EffortLevel::Default),
                "system",
                &mut copy,
            )
            .await
        };
        let cancellation = async {
            while hanging.requests().is_empty() {
                tokio::task::yield_now().await;
            }
            stop.cancel();
        };
        let (result, ()) = tokio::join!(work, cancellation);
        assert_eq!(result.unwrap_err().kind(), ErrorKind::Interrupted);
        assert_eq!(copy, small_before);
    }
}
