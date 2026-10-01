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
    model::{self, CatalogModel, ModelRequestParameters, Provider},
    sessions::{
        self, AssistantBatch, CompactionCheckpoint, SessionStore, TranscriptEntry, TurnInput,
        UserMessage, UserMessagePart,
    },
};

const SUMMARY_ALLOWANCE_BYTES: usize = model::SUMMARIZER_MAX_TOKENS * 3;
const SUMMARY_LABEL: &str = "Compaction summary of earlier conversation:\n";
const TOOL_RESULT_EXCERPT_CHARS: usize = 2_000;
const IMAGE_ESTIMATE_TOKENS: usize = 4_096;

fn to_tokens(bytes: usize) -> usize {
    bytes.div_ceil(3)
}

/// Request-size limits for one model, in estimated tokens.
pub struct Budget {
    /// The largest request estimate Ox admits or sends.
    pub admission: usize,
    /// The request estimate at which a turn compacts before its next
    /// model request.
    pub automatic_threshold: usize,
}

pub fn budget(model: &CatalogModel) -> Budget {
    let limit = model.context_limit;
    let admission = limit - 8_000.max(limit / 10);
    Budget {
        admission,
        automatic_threshold: admission * 80 / 100,
    }
}

/// The estimated tokens of the request for `transcript`.
pub fn request_tokens(
    parameters: &ModelRequestParameters,
    transcript: &[TranscriptEntry],
) -> usize {
    let body = model::ordinary_body(
        parameters,
        projection(parameters.model.provider, transcript),
    );
    to_tokens(body_bytes(body))
}

/// The bytes the request body would take, counting image data as a fixed token
/// allowance. Encoded base64 is request transport, not text for the model to
/// tokenize.
fn body_bytes(mut body: Value) -> usize {
    let mut images = 0;
    let input_key = if body.get("input").is_some() {
        "input"
    } else {
        "messages"
    };
    if let Some(messages) = body.get_mut(input_key).and_then(Value::as_array_mut) {
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
            } else if part["type"] == "input_image" {
                part["image_url"] = Value::String("[image]".to_owned());
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

/// The transcript length the latest compaction checkpoint covers.
fn summarized_prefix(transcript: &[TranscriptEntry]) -> usize {
    latest(transcript).map_or(0, |checkpoint| checkpoint.covered_prefix)
}

/// The chat messages of the next model request: the saved transcript, or the
/// latest summary followed by the entries after its covered prefix.
pub fn projection(provider: Provider, transcript: &[TranscriptEntry]) -> Vec<Value> {
    match latest(transcript) {
        Some(checkpoint) => projection_at(
            provider,
            transcript,
            checkpoint.covered_prefix,
            &checkpoint.summary,
        ),
        None => provider.transcript(transcript, None),
    }
}

/// Whether the projection of the next model request contains an image.
pub fn has_images(provider: Provider, transcript: &[TranscriptEntry]) -> bool {
    projection(provider, transcript).iter().any(|message| {
        message["content"].as_array().is_some_and(|parts| {
            parts
                .iter()
                .any(|part| matches!(part["type"].as_str(), Some("image_url" | "input_image")))
        })
    })
}

/// The index of the skill invocation to repeat after a summary covering
/// `[..cut]`: the latest turn start, when it is a covered skill invocation. A
/// long run keeps its skill instructions and arguments this way until a
/// later turn begins.
fn repeated_invocation(transcript: &[TranscriptEntry], cut: usize) -> Option<usize> {
    let (index, turn_start) = sessions::latest_turn_start(transcript)?;
    (index < cut && matches!(turn_start.input, TurnInput::SkillInvocation(_))).then_some(index)
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

fn summary_message(provider: Provider, summary: &str) -> Value {
    provider.user_message(&format!("{SUMMARY_LABEL}{summary}").into())
}

fn turn_provider_before(transcript: &[TranscriptEntry], index: usize) -> Option<Provider> {
    transcript[..index]
        .iter()
        .rev()
        .find_map(|entry| match entry {
            TranscriptEntry::TurnStart(start) => Some(start),
            _ => None,
        })
        .and_then(|start| Provider::from_qualified_model_id(&start.model))
}

fn projection_at(
    provider: Provider,
    transcript: &[TranscriptEntry],
    cut: usize,
    summary: &str,
) -> Vec<Value> {
    let mut projected = vec![summary_message(provider, summary)];
    if let Some(index) = repeated_invocation(transcript, cut) {
        projected.extend(provider.transcript(&transcript[index..=index], None));
    }
    projected
        .extend(provider.transcript(&transcript[cut..], turn_provider_before(transcript, cut)));
    projected
}

fn candidates(transcript: &[TranscriptEntry]) -> Vec<usize> {
    let start = summarized_prefix(transcript);
    transcript
        .iter()
        .enumerate()
        .skip(start)
        .filter_map(|(index, entry)| {
            matches!(entry, TranscriptEntry::AssistantBatch(_)).then_some(index + 1)
        })
        .collect()
}

/// The estimated tokens of the request when everything before `cut` is
/// replaced by `summary`.
fn projected_tokens(
    parameters: &ModelRequestParameters,
    transcript: &[TranscriptEntry],
    cut: usize,
    summary: &str,
) -> usize {
    let body = model::ordinary_body(
        parameters,
        projection_at(parameters.model.provider, transcript, cut, summary),
    );
    to_tokens(body_bytes(body))
}

/// A prospective transcript is rejected only if even the largest complete cut,
/// with room for a new summary, cannot fit the admission budget.
pub fn input_fits(parameters: &ModelRequestParameters, prospective: &[TranscriptEntry]) -> bool {
    let admission = budget(parameters.model).admission;
    if request_tokens(parameters, prospective) <= admission {
        return true;
    }
    let Some(cut) = candidates(prospective).last().copied() else {
        return false;
    };
    projected_tokens(
        parameters,
        prospective,
        cut,
        &"x".repeat(SUMMARY_ALLOWANCE_BYTES),
    ) <= admission
}

fn ranked_cuts(parameters: &ModelRequestParameters, transcript: &[TranscriptEntry]) -> Vec<usize> {
    let admission = budget(parameters.model).admission;
    let start = summarized_prefix(transcript);
    let cuts = candidates(transcript);
    let summary = summary_message(
        parameters.model.provider,
        &"x".repeat(SUMMARY_ALLOWANCE_BYTES),
    );
    let base = model::ordinary_body(parameters, vec![summary]);
    let base_bytes = body_bytes(base);
    let mut suffix_bytes = vec![0; transcript.len() - start + 1];
    for index in (start..transcript.len()).rev() {
        suffix_bytes[index - start] = suffix_bytes[index - start + 1]
            + entry_bytes(parameters.model.provider, transcript, index);
    }
    // Every cut past the latest skill invocation repeats it after the summary.
    let repeated = repeated_invocation(transcript, transcript.len()).map(|index| {
        (
            index,
            entry_bytes(parameters.model.provider, transcript, index),
        )
    });
    let mut ranked = Vec::new();
    for cut in cuts {
        let repeated_bytes = repeated
            .filter(|&(index, _)| index < cut)
            .map_or(0, |(_, bytes)| bytes);
        let estimate = to_tokens(base_bytes + suffix_bytes[cut - start] + repeated_bytes);
        if estimate > admission {
            continue;
        }
        ranked.push((cut, estimate));
    }
    ranked.sort_by_key(|(_, estimate)| *estimate);
    ranked.into_iter().map(|(cut, _)| cut).collect()
}

/// The serialized size an entry adds to a request body, counting the comma that
/// separates it from the previous message.
fn entry_bytes(provider: Provider, transcript: &[TranscriptEntry], index: usize) -> usize {
    provider
        .transcript(
            &transcript[index..=index],
            turn_provider_before(transcript, index),
        )
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

/// One labeled text value of summarizer material.
struct MaterialField {
    label: String,
    text: String,
    /// The part number the next piece gives this field. A field too large for
    /// one summarizer request is sent in parts, each piece continuing where the
    /// previous one stopped.
    part: usize,
}

impl MaterialField {
    fn new(label: String, text: String) -> Self {
        Self {
            label,
            text,
            part: 1,
        }
    }
}

/// The summarizer material for `cut`: every entry after the latest checkpoint's
/// covered prefix and before `cut`, in transcript order, each labeled with its
/// transcript index.
fn material(transcript: &[TranscriptEntry], cut: usize) -> VecDeque<MaterialField> {
    let start = summarized_prefix(transcript);
    let mut fields = VecDeque::new();
    for (index, entry) in transcript[start..cut].iter().enumerate() {
        let source = format!("Entry {}", start + index);
        match entry {
            TranscriptEntry::TurnStart(turn_start) => {
                push_turn_input(&mut fields, &source, &turn_start.input);
            }
            TranscriptEntry::AssistantBatch(batch) => push_batch(&mut fields, &source, batch),
            TranscriptEntry::SubagentMessages(messages) => {
                fields.extend(messages.iter().map(|message| {
                    MaterialField::new(
                        format!("{source} {}", message.label()),
                        message.text().to_owned(),
                    )
                }));
            }
            TranscriptEntry::CompactionCheckpoint(_) => {}
        }
    }
    fields
}

fn push_turn_input(fields: &mut VecDeque<MaterialField>, source: &str, input: &TurnInput) {
    match input {
        TurnInput::UserMessage(message) => push_user_request(fields, source, message),
        TurnInput::SkillInvocation(invocation) => {
            push_user_request(fields, source, &model::skill_invocation_message(invocation))
        }
    }
}

fn push_user_request(fields: &mut VecDeque<MaterialField>, source: &str, message: &UserMessage) {
    for part in &message.parts {
        let text = match part {
            UserMessagePart::Text(text) => text.clone(),
            UserMessagePart::Image(image) => format!("[image: {}]", image.mime_type),
        };
        fields.push_back(MaterialField::new(format!("{source} user request"), text));
    }
}

fn push_batch(fields: &mut VecDeque<MaterialField>, source: &str, batch: &AssistantBatch) {
    let message = &batch.message;
    if !message.text.is_empty() {
        fields.push_back(MaterialField::new(
            format!("{source} assistant answer"),
            message.text.clone(),
        ));
    }
    for call in &message.tool_calls {
        if !call.arguments.is_empty() {
            fields.push_back(MaterialField::new(
                format!("{source} tool {} arguments", call.name),
                call.arguments.clone(),
            ));
        }
    }
    for (call, outcome) in message.tool_calls.iter().zip(&batch.outcomes) {
        if !outcome.text.is_empty() {
            fields.push_back(MaterialField::new(
                format!(
                    "{source} tool {} {} outcome",
                    call.name,
                    outcome.status.id()
                ),
                tool_result_excerpt(&outcome.text),
            ));
        }
    }
}

/// The bytes `text` adds to a serialized request body inside a JSON string.
fn escaped_bytes(text: &str) -> usize {
    serde_json::to_string(text).expect("text serializes").len() - 2
}

/// Fills one piece with as much of `fields` as fits in a summarizer request
/// beside `previous`, removing what it takes. Whole fields go in while they
/// fit. The first field that does not fit contributes its longest prefix that
/// does, found by halving, and the rest of it stays at the front of `fields`
/// as its next part.
///
/// A piece adds exactly its escaped length to the summarizer body, so the body
/// is measured once without one.
fn next_piece(
    model: &CatalogModel,
    previous: &str,
    fields: &mut VecDeque<MaterialField>,
) -> io::Result<String> {
    let body = model::summarizer_body(model, previous, "");
    let body_bytes = serde_json::to_vec(&body)
        .expect("summary body serializes")
        .len();
    let mut piece = String::new();
    let mut piece_bytes = 0;
    while let Some(field) = fields.front_mut() {
        let header = format!("{}, part {}:\n", field.label, field.part);
        let fits = |bytes| {
            to_tokens(body_bytes + piece_bytes + bytes) + model::SUMMARIZER_MAX_TOKENS
                <= model.context_limit
        };
        let (take, addition) = fitting_prefix(&header, &field.text, fits);
        if take == 0 {
            if piece.is_empty() {
                return Err(io::Error::new(
                    ErrorKind::InvalidInput,
                    "summarizer has no room for conversation material",
                ));
            }
            break;
        }
        piece_bytes += escaped_bytes(&addition);
        piece.push_str(&addition);
        if take == field.text.len() {
            fields.pop_front();
        } else {
            field.text.drain(..take);
            field.part += 1;
        }
    }
    Ok(piece)
}

/// The longest prefix of `text` that halving from its full length finds to
/// fit, with the labeled addition it makes to a piece. A zero-length prefix
/// means none of `text` fits.
fn fitting_prefix(header: &str, text: &str, fits: impl Fn(usize) -> bool) -> (usize, String) {
    let mut take = text.len();
    loop {
        while !text.is_char_boundary(take) {
            take -= 1;
        }
        let addition = format!("{header}{}\n", &text[..take]);
        if take == 0 || fits(escaped_bytes(&addition)) {
            return (take, addition);
        }
        take /= 2;
    }
}

/// Compacts `transcript` if a cut of it produces a smaller request that fits
/// the admission budget, and returns whether a checkpoint was committed.
/// Rejected cuts still spend summarizer requests, and their cost is saved with
/// the committed checkpoint. The checkpoint is pushed onto `transcript` only
/// after the store saves it.
pub async fn compact(
    store: &SessionStore,
    clients: &model::Clients,
    cancellation: &PromptCancellation,
    id: &SessionId,
    parameters: &ModelRequestParameters,
    transcript: &mut Vec<TranscriptEntry>,
) -> io::Result<bool> {
    let model = parameters.model;
    let original = request_tokens(parameters, transcript);
    // Every summarizer request counts, including those for rejected cuts.
    let mut summarizer_cost: Option<f64> = None;
    for cut in ranked_cuts(parameters, transcript) {
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
                result = clients.summarize(model, &summary, &piece) => result?,
            };
            summary = next;
            if let Some(cost) = usage.and_then(|usage| usage.cost) {
                *summarizer_cost.get_or_insert(0.0) += cost;
            }
        }
        if cancellation.is_cancelled() {
            return Err(io::Error::new(
                ErrorKind::Interrupted,
                "compaction cancelled",
            ));
        }
        let actual = projected_tokens(parameters, transcript, cut, &summary);
        if actual >= original || actual > budget(model).admission {
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

    #[tokio::test]
    async fn an_oversized_openai_summary_never_becomes_a_checkpoint() {
        let parameters = ModelRequestParameters::new(
            crate::openai::fixture::DEFAULT_MODEL,
            EffortLevel::Low,
            "You are Ox.".to_owned(),
            tools::Role::Main,
        )
        .unwrap();
        let store = SessionStore::in_memory();
        let id = store.create(std::path::Path::new("/workspace")).unwrap().id;
        let mut start = TurnStart::test("Task".to_owned());
        start.model = crate::openai::fixture::DEFAULT_MODEL.to_owned();
        store.append_turn_start(&id, &start).unwrap();
        let batch = AssistantBatch::new(
            AssistantMessage {
                text: "x".repeat(700000),
                reasoning: String::new(),
                tool_calls: vec![],
                continuation_metadata: vec![],
                usage: None,
            },
            vec![],
        )
        .unwrap();
        store.append_batch(&id, &batch).unwrap();
        let mut transcript = store.read(&id).unwrap().unwrap().transcript;
        let original = transcript.clone();
        let server =
            crate::openai::fixture::Server::start(vec![crate::openai::fixture::text_reply(
                &"oversized".repeat(100000),
            )])
            .await;
        assert!(
            !compact(
                &store,
                &server.client().into(),
                &PromptCancellation::new(),
                &id,
                &parameters,
                &mut transcript
            )
            .await
            .unwrap()
        );
        assert_eq!(transcript, original);
        assert_eq!(store.read(&id).unwrap().unwrap().transcript, original);
    }

    #[test]
    fn openai_request_estimates_count_images_without_tokenizing_base64() {
        let parameters = ModelRequestParameters::new(
            crate::openai::fixture::DEFAULT_MODEL,
            EffortLevel::Low,
            "You are Ox.".to_owned(),
            tools::Role::Main,
        )
        .unwrap();
        let transcript = |data: &str| {
            vec![TranscriptEntry::turn(UserMessage {
                parts: vec![UserMessagePart::Image(ImageAttachment {
                    mime_type: "image/png".to_owned(),
                    data: data.to_owned(),
                })],
            })]
        };
        let small = transcript("YWJj");
        let large = transcript(&"YWJj".repeat(20000));
        assert_eq!(
            request_tokens(&parameters, &small),
            request_tokens(&parameters, &large)
        );
        assert!(has_images(Provider::OpenAI, &small));
        let mut body = model::ordinary_body(&parameters, projection(Provider::OpenAI, &small));
        body["input"][0]["content"][0]["image_url"] = serde_json::json!("[image]");
        assert_eq!(
            request_tokens(&parameters, &small),
            to_tokens(serde_json::to_vec(&body).unwrap().len() + IMAGE_ESTIMATE_TOKENS * 3)
        );
    }
    use crate::{
        openrouter::fixture::{DEFAULT_MODEL, Reply, Server, text_reply},
        sessions::{
            AssistantMessage, EffortLevel, ImageAttachment, SkillInvocation, ToolCall, ToolOutcome,
            TurnStart,
        },
        tools,
    };

    fn parameters() -> ModelRequestParameters {
        ModelRequestParameters::new(
            DEFAULT_MODEL,
            EffortLevel::Default,
            "system".to_owned(),
            tools::Role::Main,
        )
        .unwrap()
    }

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
            vec![ToolOutcome::completed(output)],
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
                &TurnStart::test(SkillInvocation {
                    name: "goal".to_owned(),
                    arguments: "Record the old details.".to_owned(),
                    instructions: "Record details accurately.".to_owned(),
                    images: vec![],
                }),
            )
            .unwrap();
        store
            .append_batch(
                &id,
                &answer(&format!("Recorded {}", "old details ".repeat(3000))),
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
            request_tokens(&parameters(), &transcript)
                < budget(parameters().model).automatic_threshold,
            "manual compaction is below the automatic threshold"
        );
        assert!(
            compact(
                &store,
                &server.client().into(),
                &cancellation,
                &id,
                &parameters(),
                &mut transcript
            )
            .await
            .unwrap()
        );
        let first_cut = transcript.len() - 1;
        store
            .append_turn_start(&id, &TurnStart::test("active request".to_owned()))
            .unwrap();
        transcript.push(TranscriptEntry::turn("active request".to_owned()));
        let batch = tool_batch("first", &"new details ".repeat(3000));
        store.append_batch(&id, &batch).unwrap();
        transcript.push(TranscriptEntry::AssistantBatch(batch));
        assert!(
            compact(
                &store,
                &server.client().into(),
                &cancellation,
                &id,
                &parameters(),
                &mut transcript
            )
            .await
            .unwrap()
        );
        let batch = tool_batch("second", &"later details ".repeat(3000));
        store.append_batch(&id, &batch).unwrap();
        transcript.push(TranscriptEntry::AssistantBatch(batch));
        assert!(
            compact(
                &store,
                &server.client().into(),
                &cancellation,
                &id,
                &parameters(),
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
        assert_eq!(
            projection(Provider::OpenRouter, &transcript),
            vec![serde_json::json!({
                "role": "user",
                "content": "Compaction summary of earlier conversation:\nActive request carried; next tool complete.",
            })]
        );
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
        assert!(material(0).contains("Entry 0 user request, part 1:\nSkill /goal invoked."));
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
    }

    #[test]
    fn requests_send_the_latest_summary_followed_by_the_entries_it_does_not_cover() {
        let checkpoint = |summary: &str, covered_prefix| {
            TranscriptEntry::CompactionCheckpoint(CompactionCheckpoint {
                summary: summary.to_owned(),
                covered_prefix,
                summarizer_cost: None,
            })
        };
        let transcript = vec![
            TranscriptEntry::turn("first".to_owned()),
            TranscriptEntry::AssistantBatch(answer("first answer")),
            checkpoint("Older summary", 2),
            TranscriptEntry::turn("middle".to_owned()),
            TranscriptEntry::AssistantBatch(answer("middle answer")),
            checkpoint("Current summary", 5),
            TranscriptEntry::turn("recent".to_owned()),
            TranscriptEntry::AssistantBatch(answer("recent answer")),
        ];
        let projected = projection(Provider::OpenRouter, &transcript);
        assert_eq!(projected.len(), 3);
        assert_eq!(
            projected[0],
            serde_json::json!({
                "role": "user",
                "content": "Compaction summary of earlier conversation:\nCurrent summary",
            })
        );
        assert_eq!(
            projected[1],
            serde_json::json!({ "role": "user", "content": "recent" })
        );
        assert_eq!(projected[2]["content"], "recent answer");
    }

    #[test]
    fn compacted_suffixes_keep_turn_provider_for_metadata_and_byte_estimates() {
        for source in [Provider::OpenRouter, Provider::OpenAI] {
            let mut start = TurnStart::test("request".to_owned());
            start.model = match source {
                Provider::OpenRouter => DEFAULT_MODEL,
                Provider::OpenAI => crate::openai::fixture::DEFAULT_MODEL,
            }
            .to_owned();
            let transcript = vec![
                TranscriptEntry::TurnStart(start),
                TranscriptEntry::AssistantBatch(answer("first answer")),
                TranscriptEntry::AssistantBatch(answer("second answer")),
            ];
            let cut = 2;
            for target in [Provider::OpenRouter, Provider::OpenAI] {
                let parameters = match target {
                    Provider::OpenRouter => parameters(),
                    Provider::OpenAI => ModelRequestParameters::new(
                        crate::openai::fixture::DEFAULT_MODEL,
                        EffortLevel::Default,
                        "system".to_owned(),
                        tools::Role::Main,
                    )
                    .unwrap(),
                };
                let summary = "summary";
                let projected = projection_at(target, &transcript, cut, summary);
                let has_metadata = match target {
                    Provider::OpenRouter => projected
                        .iter()
                        .any(|item| item.get("reasoning_details").is_some()),
                    Provider::OpenAI => projected
                        .iter()
                        .any(|item| item.get("opaque") == Some(&Value::Bool(true))),
                };
                assert_eq!(has_metadata, source == target, "{source:?} to {target:?}");
                assert!(projected.iter().any(|item| match target {
                    Provider::OpenRouter => item["content"] == "second answer",
                    Provider::OpenAI => item["content"][0]["text"] == "second answer",
                }));

                let base = body_bytes(model::ordinary_body(
                    &parameters,
                    vec![summary_message(target, summary)],
                ));
                let suffix = entry_bytes(target, &transcript, cut);
                assert_eq!(
                    base + suffix,
                    body_bytes(model::ordinary_body(&parameters, projected)),
                    "{source:?} to {target:?}"
                );
            }
        }
    }

    #[test]
    fn ranked_cuts_follow_the_latest_checkpoint_smallest_request_first() {
        let transcript = vec![
            TranscriptEntry::turn("earlier request".to_owned()),
            TranscriptEntry::AssistantBatch(answer("earlier answer")),
            TranscriptEntry::CompactionCheckpoint(CompactionCheckpoint {
                summary: "Earlier work is complete.".to_owned(),
                covered_prefix: 2,
                summarizer_cost: None,
            }),
            TranscriptEntry::turn("current request".to_owned()),
            TranscriptEntry::AssistantBatch(tool_batch("rank", "tool output")),
            TranscriptEntry::AssistantBatch(answer("latest answer")),
        ];
        let cuts = ranked_cuts(&parameters(), &transcript);
        assert_eq!(cuts, [6, 5]);
        let summary = "x".repeat(SUMMARY_ALLOWANCE_BYTES);
        assert!(
            projected_tokens(&parameters(), &transcript, cuts[0], &summary)
                < projected_tokens(&parameters(), &transcript, cuts[1], &summary)
        );
        let base = body_bytes(model::ordinary_body(
            &parameters(),
            vec![summary_message(Provider::OpenRouter, &summary)],
        ));
        for &cut in &cuts {
            let suffix: usize = (cut..transcript.len())
                .map(|index| entry_bytes(Provider::OpenRouter, &transcript, index))
                .sum();
            assert_eq!(
                base + suffix,
                body_bytes(model::ordinary_body(
                    &parameters(),
                    projection_at(Provider::OpenRouter, &transcript, cut, &summary)
                )),
                "ranking counts the bytes of the projected request for cut {cut}"
            );
        }
    }

    /// A user message and a skill invocation, each with one image.
    fn image_transcript() -> Vec<TranscriptEntry> {
        vec![
            TranscriptEntry::turn(UserMessage {
                parts: vec![
                    UserMessagePart::Text("Inspect this".to_owned()),
                    UserMessagePart::Image(ImageAttachment {
                        data: "aGVsbG8=".to_owned(),
                        mime_type: "image/png".to_owned(),
                    }),
                ],
            }),
            TranscriptEntry::turn(SkillInvocation {
                name: "look".to_owned(),
                arguments: "closely".to_owned(),
                instructions: "Describe the image.".to_owned(),
                images: vec![ImageAttachment {
                    data: "d29ybGQ=".to_owned(),
                    mime_type: "image/jpeg".to_owned(),
                }],
            }),
        ]
    }

    #[test]
    fn images_leave_the_projection_once_a_checkpoint_covers_them() {
        let [user, skill] = image_transcript().try_into().unwrap();
        let answered = || TranscriptEntry::AssistantBatch(answer("Seen."));
        let checkpoint = || {
            TranscriptEntry::CompactionCheckpoint(CompactionCheckpoint {
                summary: "The image was inspected.".to_owned(),
                covered_prefix: 2,
                summarizer_cost: None,
            })
        };
        for (case, transcript, expected) in [
            ("an uncovered image", vec![user.clone()], true),
            (
                "a covered user message",
                vec![user, answered(), checkpoint()],
                false,
            ),
            (
                "a covered skill invocation repeated after the summary",
                vec![skill, answered(), checkpoint()],
                true,
            ),
        ] {
            assert_eq!(
                has_images(Provider::OpenRouter, &transcript),
                expected,
                "{case}"
            );
        }
    }

    #[test]
    fn subagent_messages_reach_requests_and_summarizer_material_with_their_attribution() {
        use crate::sessions::{SubagentMessage, SubagentMessageContent};
        let transcript = vec![
            TranscriptEntry::turn("Delegate.".to_owned()),
            TranscriptEntry::AssistantBatch(answer("Waiting.")),
            TranscriptEntry::SubagentMessages(vec![
                SubagentMessage {
                    subagent_id: "child-1".to_owned(),
                    content: SubagentMessageContent::FinalAnswer("Fixed the parser.".to_owned()),
                },
                SubagentMessage {
                    subagent_id: "child-2".to_owned(),
                    content: SubagentMessageContent::Failure("The model refused.".to_owned()),
                },
            ]),
        ];
        assert_eq!(
            projection(Provider::OpenRouter, &transcript)[2..],
            [
                serde_json::json!({"role": "user", "content": "Final answer from subagent child-1:\nFixed the parser."}),
                serde_json::json!({"role": "user", "content": "Failure of subagent child-2:\nThe model refused."}),
            ]
        );
        let fields = material(&transcript, 3);
        let labeled: Vec<_> = fields
            .iter()
            .skip(2)
            .map(|field| (field.label.as_str(), field.text.as_str()))
            .collect();
        assert_eq!(
            labeled,
            [
                (
                    "Entry 2 Final answer from subagent child-1",
                    "Fixed the parser."
                ),
                ("Entry 2 Failure of subagent child-2", "The model refused."),
            ]
        );
    }

    #[test]
    fn summarizer_material_describes_images_by_mime_type() {
        let fields = material(&image_transcript(), 2);
        let request = |label: &str, value: &str| {
            fields
                .iter()
                .any(|field| field.label == label && field.text.starts_with(value))
        };
        assert!(request("Entry 0 user request", "[image: image/png]"));
        assert!(request("Entry 1 user request", "Skill /look invoked."));
        assert!(request("Entry 1 user request", "[image: image/jpeg]"));
        assert!(
            fields
                .iter()
                .all(|field| !field.text.contains("aGVsbG8=") && !field.text.contains("d29ybGQ="))
        );
    }

    #[test]
    fn summarizer_pieces_fit_the_context_limit_and_keep_all_material() {
        let model = model::catalog_model("openrouter:acme/plain").unwrap();
        let previous = "Earlier \"summary\".";
        let text = "Quote \" slash \\ tab \t bell \u{7} snow 雪\n".repeat(1_000);
        let transcript = vec![TranscriptEntry::turn(text.clone())];
        let mut fields = material(&transcript, 1);
        let mut rebuilt = String::new();
        let mut pieces = 0;
        let mut part = 0;
        while !fields.is_empty() {
            let piece = next_piece(model, previous, &mut fields).unwrap();
            pieces += 1;
            let body = model::summarizer_body(model, previous, &piece);
            assert!(
                to_tokens(serde_json::to_vec(&body).unwrap().len()) + model::SUMMARIZER_MAX_TOKENS
                    <= model.context_limit,
                "piece {pieces} fits the context limit"
            );
            let mut rest = piece.as_str();
            while !rest.is_empty() {
                part += 1;
                rest = rest
                    .strip_prefix(&format!("Entry 0 user request, part {part}:\n"))
                    .expect("parts continue in order");
                let end = rest
                    .find("Entry 0 user request, part ")
                    .unwrap_or(rest.len());
                rebuilt.push_str(&rest[..end - 1]);
                rest = &rest[end..];
            }
        }
        assert!(pieces > 1, "the material needs several pieces");
        assert_eq!(rebuilt, text);
    }

    #[test]
    fn projected_images_count_as_a_fixed_allowance() {
        let mut transcript = image_transcript();
        assert_eq!(
            projection_at(Provider::OpenRouter, &transcript, 0, "summary")[1]["content"][1]["type"],
            "image_url"
        );
        let estimate = request_tokens(&parameters(), &transcript);
        if let TranscriptEntry::TurnStart(TurnStart {
            input: TurnInput::UserMessage(message),
            ..
        }) = &mut transcript[0]
            && let UserMessagePart::Image(image) = &mut message.parts[1]
        {
            image.data = "A".repeat(4_000);
        }
        assert_eq!(estimate, request_tokens(&parameters(), &transcript));
    }

    #[tokio::test]
    async fn compaction_brings_an_oversized_request_within_admission() {
        let store = SessionStore::in_memory();
        let id = store.create(std::path::Path::new("/workspace")).unwrap().id;
        store
            .append_turn_start(&id, &TurnStart::test("o".repeat(1_500_000)))
            .unwrap();
        store.append_batch(&id, &answer("older work done")).unwrap();
        store
            .append_turn_start(&id, &TurnStart::test("u".repeat(1_800_000)))
            .unwrap();
        let mut transcript = store.read(&id).unwrap().unwrap().transcript;
        let original = request_tokens(&parameters(), &transcript);
        let server = Server::start(vec![text_reply("Older work summarized.")]).await;
        assert!(
            compact(
                &store,
                &server.client().into(),
                &PromptCancellation::new(),
                &id,
                &parameters(),
                &mut transcript
            )
            .await
            .unwrap()
        );
        let admission = budget(parameters().model).admission;
        let estimate = request_tokens(&parameters(), &transcript);
        assert!(
            estimate < original && estimate <= admission,
            "compaction reduces the request and admits the remaining input"
        );
    }

    #[tokio::test]
    async fn a_later_summary_failure_leaves_the_old_checkpoint_active() {
        let store = SessionStore::in_memory();
        let id = store.create(std::path::Path::new("/workspace")).unwrap().id;
        store
            .append_turn_start(&id, &TurnStart::test("earlier work".to_owned()))
            .unwrap();
        store.append_batch(&id, &answer("earlier answer")).unwrap();
        store
            .append_checkpoint(
                &id,
                2,
                &CompactionCheckpoint {
                    summary: "Earlier work is complete.".to_owned(),
                    covered_prefix: 2,
                    summarizer_cost: None,
                },
            )
            .unwrap();
        let large = "x".repeat(3_300_000);
        store
            .append_turn_start(&id, &TurnStart::test(large.clone()))
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
                &server.client().into(),
                &PromptCancellation::new(),
                &id,
                &parameters(),
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
            .append_turn_start(&small_id, &TurnStart::test("older ".repeat(4000)))
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
                    &server.client().into(),
                    &PromptCancellation::new(),
                    &small_id,
                    &parameters(),
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
                &server.client().into(),
                &PromptCancellation::new(),
                &small_id,
                &parameters(),
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
                &hanging.client().into(),
                &cancel,
                &small_id,
                &parameters(),
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
