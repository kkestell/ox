//! What keeps a Claude session busy between turns: tasks in Claude's live
//! background set, and scheduled prompts the bridge infers from Claude's
//! scheduling tools. The client gets both as the `background` list of every
//! state update.

use std::collections::{BTreeSet, HashMap, HashSet};

use agent_client_protocol::v2::schema::{BackgroundToolCall, BackgroundWork, OtherBackgroundWork};
use chrono::DateTime;
use serde_json::{Value, json};

/// A recurring job expires this long after its creation.
const RECURRING_LIFETIME_MS: u64 = 7 * 24 * 60 * 60 * 1000;
/// A self-paced loop iteration that schedules nothing gets a wakeup this
/// long after it ends.
const FALLBACK_DELAY_MS: u64 = 20 * 60 * 1000;
const SCHEDULING_TOOLS: [&str; 3] = ["CronCreate", "CronDelete", "ScheduleWakeup"];

/// Milliseconds since the Unix epoch, which Claude's schedule times use.
pub fn now_ms() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |elapsed| {
            u64::try_from(elapsed.as_millis()).unwrap_or(u64::MAX)
        })
}

#[derive(Debug, PartialEq)]
enum Kind {
    Recurring {
        created: u64,
    },
    OneShot {
        fire_at: u64,
    },
    Wakeup {
        fire_at: u64,
        fallback: bool,
    },
    /// A scheduling call the bridge could not read; it counts until claude
    /// exits.
    Unknown,
}

#[derive(Debug)]
struct Schedule {
    /// The job's ID, or the ID of the tool call that made the schedule.
    key: String,
    kind: Kind,
    /// The main-thread tool call that made the schedule, which stays
    /// `pending` while the schedule lasts.
    tool_call: Option<String>,
}

/// A scheduling tool call waiting for its result.
struct Call {
    name: String,
    input: Value,
    main: bool,
}

#[derive(Default)]
pub struct Background {
    /// Claude's live background tasks, by task ID.
    tasks: BTreeSet<String>,
    /// The tool call of every task Claude started, by task ID. Claude lists
    /// a task before it says which tool call started it.
    started: HashMap<String, String>,
    schedules: Vec<Schedule>,
    calls: HashMap<String, Call>,
    /// The running turn called `ScheduleWakeup`.
    wakeup_called: bool,
}

impl Background {
    pub fn task_started(&mut self, task_id: &str, tool_use_id: &str) {
        self.started
            .insert(task_id.to_owned(), tool_use_id.to_owned());
    }

    /// Replaces the live tasks with Claude's latest set.
    pub fn set_tasks(&mut self, tasks: Vec<String>) {
        self.tasks = tasks.into_iter().collect();
    }

    /// The tool calls that started the live tasks.
    fn task_tool_calls(&self) -> impl Iterator<Item = &String> {
        self.tasks.iter().filter_map(|id| self.started.get(id))
    }

    /// Forgets everything, as when claude exits.
    pub fn clear(&mut self) {
        *self = Self::default();
    }

    /// The tool calls that stand for listed work, with the status each
    /// keeps while the work lasts: tasks are `in_progress`, schedules
    /// `pending`.
    pub fn held_status(&self, tool_call: &str) -> Option<HeldStatus> {
        if self.task_tool_calls().any(|t| t == tool_call) {
            Some(HeldStatus::InProgress)
        } else if self
            .schedules
            .iter()
            .any(|s| s.tool_call.as_deref() == Some(tool_call))
        {
            Some(HeldStatus::Pending)
        } else {
            None
        }
    }

    /// Every tool call that stands for listed work.
    pub fn tool_calls(&self) -> HashSet<String> {
        self.task_tool_calls()
            .cloned()
            .chain(self.schedules.iter().filter_map(|s| s.tool_call.clone()))
            .collect()
    }

    /// The ACP list. Work whose tool call `live` accepts is listed as that
    /// tool call, once; the rest as custom items.
    pub fn items(&self, live: impl Fn(&str) -> bool) -> Vec<BackgroundWork> {
        let mut listed = HashSet::new();
        let mut items = Vec::new();
        let work = self
            .tasks
            .iter()
            .map(|id| (self.started.get(id), "_claude_task", "taskId", id))
            .chain(
                self.schedules
                    .iter()
                    .map(|s| (s.tool_call.as_ref(), "_claude_schedule", "id", &s.key)),
            );
        for (tool, kind, field, id) in work {
            match tool {
                Some(tool) if live(tool) => {
                    if listed.insert(tool.clone()) {
                        items.push(BackgroundWork::ToolCall(BackgroundToolCall::new(
                            tool.clone(),
                        )));
                    }
                }
                _ => items.push(BackgroundWork::Other(OtherBackgroundWork::new(
                    kind,
                    [(field.to_owned(), json!(id))].into(),
                ))),
            }
        }
        items
    }

    /// Notes the scheduling tool calls and results of a message, which is
    /// an `assistant` or `user` line of claude's output or the same entry in
    /// a transcript. `at` is when it happened. Returns whether the
    /// schedules changed.
    pub fn observe(&mut self, entry: &Value, at: u64, main: bool) -> bool {
        let Some(blocks) = entry["message"]["content"].as_array() else {
            return false;
        };
        let results = blocks.iter().filter(|b| b["type"] == "tool_result").count();
        let result = if entry["tool_use_result"].is_null() {
            &entry["toolUseResult"]
        } else {
            &entry["tool_use_result"]
        };
        let mut changed = false;
        for block in blocks {
            match block["type"].as_str() {
                Some("tool_use") => {
                    let (Some(id), Some(name)) = (block["id"].as_str(), block["name"].as_str())
                    else {
                        continue;
                    };
                    if !SCHEDULING_TOOLS.contains(&name) {
                        continue;
                    }
                    if name == "ScheduleWakeup" {
                        self.wakeup_called = true;
                    }
                    self.calls.insert(
                        id.to_owned(),
                        Call {
                            name: name.to_owned(),
                            input: block["input"].clone(),
                            main,
                        },
                    );
                }
                Some("tool_result") => {
                    let Some(id) = block["tool_use_id"].as_str() else {
                        continue;
                    };
                    let Some(call) = self.calls.remove(id) else {
                        continue;
                    };
                    // A result shared with other tool results in one message
                    // cannot be told apart.
                    let readable = (results == 1 && block["is_error"] != true && !result.is_null())
                        .then_some(result);
                    changed |= self.finish(id, &call, readable, at);
                }
                _ => {}
            }
        }
        changed
    }

    fn finish(&mut self, id: &str, call: &Call, result: Option<&Value>, at: u64) -> bool {
        let tool_call = call.main.then(|| id.to_owned());
        let kind_and_key = match (call.name.as_str(), result) {
            ("CronCreate", Some(result)) => (|| {
                let key = result["id"].as_str()?.to_owned();
                let fire_at = millis(&result["nextFireAt"])?;
                let recurring = result["recurring"]
                    .as_bool()
                    .or(call.input["recurring"].as_bool())
                    .unwrap_or(true);
                let kind = if recurring {
                    Kind::Recurring { created: at }
                } else {
                    Kind::OneShot { fire_at }
                };
                Some((kind, key))
            })(),
            ("CronDelete", Some(_)) => {
                let Some(key) = call.input["id"].as_str() else {
                    return self.add_unknown(id);
                };
                let before = self.schedules.len();
                self.schedules.retain(|s| {
                    s.key != key || !matches!(s.kind, Kind::Recurring { .. } | Kind::OneShot { .. })
                });
                return self.schedules.len() != before;
            }
            // A stop that went through drops every wakeup and fallback.
            ("ScheduleWakeup", Some(_)) if call.input["stop"] == true => {
                let before = self.schedules.len();
                self.schedules
                    .retain(|s| !matches!(s.kind, Kind::Wakeup { .. }));
                return self.schedules.len() != before;
            }
            ("ScheduleWakeup", Some(result)) => millis(&result["scheduledFor"]).map(|fire_at| {
                (
                    Kind::Wakeup {
                        fire_at,
                        fallback: false,
                    },
                    id.to_owned(),
                )
            }),
            _ => None,
        };
        match kind_and_key {
            Some((kind, key)) => {
                self.schedules.push(Schedule {
                    key,
                    kind,
                    tool_call,
                });
                true
            }
            None => self.add_unknown(id),
        }
    }

    fn add_unknown(&mut self, id: &str) -> bool {
        self.schedules.push(Schedule {
            key: id.to_owned(),
            kind: Kind::Unknown,
            tool_call: None,
        });
        true
    }

    /// Drops the schedules that a turn from `started` to `ended` fulfilled.
    /// A wakeup the turn ended without scheduling another leaves the loop an
    /// implied fallback wakeup. Returns whether the schedules changed.
    pub fn turn_ended(&mut self, started: u64, ended: u64) -> bool {
        let before = self.schedules.len();
        let mut wakeup = false;
        self.schedules.retain(|schedule| match schedule.kind {
            Kind::Recurring { created } => started < created + RECURRING_LIFETIME_MS,
            Kind::OneShot { fire_at } => started < fire_at,
            Kind::Wakeup {
                fire_at,
                fallback: implied,
            } => {
                let due = started >= fire_at;
                wakeup |= due && !implied;
                !due
            }
            Kind::Unknown => true,
        });
        let mut changed = self.schedules.len() != before;
        if wakeup && !self.wakeup_called {
            self.schedules.push(Schedule {
                key: format!("fallback-{ended}"),
                kind: Kind::Wakeup {
                    fire_at: ended + FALLBACK_DELAY_MS,
                    fallback: true,
                },
                tool_call: None,
            });
            changed = true;
        }
        self.wakeup_called = false;
        changed
    }

    /// Seeds the schedules from a transcript's tool calls, keeping what
    /// `claude --resume` restores at `now`: recurring jobs that have not
    /// expired and one-shot jobs not yet due.
    pub fn seed(&mut self, entries: &[Value], now: u64) {
        for entry in entries {
            let at = entry["timestamp"]
                .as_str()
                .and_then(|time| DateTime::parse_from_rfc3339(time).ok())
                .map_or(now, |time| {
                    u64::try_from(time.timestamp_millis()).unwrap_or(0)
                });
            self.observe(entry, at, false);
        }
        self.schedules.retain(|s| match s.kind {
            Kind::Recurring { created } => now < created + RECURRING_LIFETIME_MS,
            Kind::OneShot { fire_at } => fire_at > now,
            Kind::Wakeup { .. } | Kind::Unknown => false,
        });
        self.calls.clear();
        self.wakeup_called = false;
    }
}

/// The status a tool call keeps while its work is listed.
#[derive(Clone, Copy, PartialEq, Debug)]
pub enum HeldStatus {
    Pending,
    InProgress,
}

#[allow(
    clippy::cast_possible_truncation,
    clippy::cast_sign_loss,
    reason = "Claude may write a time as a float, and whole milliseconds are enough"
)]
fn millis(value: &Value) -> Option<u64> {
    value.as_u64().or_else(|| {
        value
            .as_f64()
            .filter(|time| *time >= 0.0)
            .map(|time| time as u64)
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn call(name: &str, id: &str, input: &Value) -> Value {
        json!({"type": "assistant", "message": {"content": [
            {"type": "tool_use", "id": id, "name": name, "input": input}
        ]}})
    }

    fn result(id: &str, output: &Value) -> Value {
        json!({"type": "user", "tool_use_result": output, "message": {"content": [
            {"type": "tool_result", "tool_use_id": id, "content": "ok"}
        ]}})
    }

    #[allow(
        clippy::needless_pass_by_value,
        reason = "the tests pass `json!` values"
    )]
    fn run(
        background: &mut Background,
        name: &str,
        id: &str,
        input: Value,
        output: Value,
        at: u64,
    ) {
        background.observe(&call(name, id, &input), at, true);
        background.observe(&result(id, &output), at, true);
    }

    fn keys(background: &Background) -> Vec<&str> {
        background
            .schedules
            .iter()
            .map(|s| s.key.as_str())
            .collect()
    }

    #[test]
    fn work_is_listed_once_as_a_tool_call_or_a_custom_item() {
        let mut background = Background::default();
        background.set_tasks(vec!["t1".into(), "t2".into(), "t3".into()]);
        background.task_started("t1", "tool_1");
        background.task_started("t2", "tool_1");
        let items = background.items(|tool| tool == "tool_1");
        assert_eq!(
            serde_json::to_value(&items).unwrap(),
            json!([
                {"type": "tool_call", "toolCallId": "tool_1"},
                {"type": "_claude_task", "taskId": "t3"},
            ])
        );
        let items = background.items(|_| false);
        assert_eq!(items.len(), 3);
        assert_eq!(
            serde_json::to_value(&items[0]).unwrap(),
            json!({"type": "_claude_task", "taskId": "t1"})
        );
    }

    #[test]
    fn a_recurring_job_lasts_until_deleted_or_expired() {
        let mut background = Background::default();
        let created = 1_000;
        run(
            &mut background,
            "CronCreate",
            "c1",
            json!({"cron": "* * * * *", "prompt": "x"}),
            json!({"id": "job1", "recurring": true, "nextFireAt": 5_000}),
            created,
        );
        assert_eq!(keys(&background), ["job1"]);
        background.turn_ended(
            created + RECURRING_LIFETIME_MS - 1,
            created + RECURRING_LIFETIME_MS,
        );
        assert_eq!(keys(&background), ["job1"]);
        run(
            &mut background,
            "CronDelete",
            "c2",
            json!({"id": "job1"}),
            json!({"id": "job1"}),
            2_000,
        );
        assert!(keys(&background).is_empty());

        run(
            &mut background,
            "CronCreate",
            "c3",
            json!({"cron": "* * * * *", "prompt": "x"}),
            json!({"id": "job2", "recurring": true, "nextFireAt": 5_000}),
            created,
        );
        background.turn_ended(
            created + RECURRING_LIFETIME_MS,
            created + RECURRING_LIFETIME_MS + 1,
        );
        assert!(keys(&background).is_empty());
    }

    #[test]
    fn a_one_shot_job_lasts_until_a_turn_starting_after_its_fire_time_ends() {
        let mut background = Background::default();
        run(
            &mut background,
            "CronCreate",
            "c1",
            json!({}),
            json!({"id": "once", "recurring": false, "nextFireAt": 10_000}),
            1_000,
        );
        background.turn_ended(1_000, 20_000);
        assert_eq!(keys(&background), ["once"]);
        background.turn_ended(10_000, 20_000);
        assert!(keys(&background).is_empty());
    }

    #[test]
    fn a_wakeup_left_unrescheduled_leaves_one_fallback() {
        let mut background = Background::default();
        run(
            &mut background,
            "ScheduleWakeup",
            "w1",
            json!({"delaySeconds": 60}),
            json!({"scheduledFor": 10_000}),
            1_000,
        );
        assert_eq!(keys(&background), ["w1"]);
        background.turn_ended(1_000, 2_000);
        assert_eq!(keys(&background), ["w1"]);
        // The turn that ends the wakeup schedules nothing.
        background.turn_ended(10_000, 11_000);
        assert_eq!(keys(&background), ["fallback-11000"]);
        // Ending the fallback without a wakeup call ends the loop.
        background.turn_ended(11_000 + FALLBACK_DELAY_MS, 11_000 + FALLBACK_DELAY_MS + 1);
        assert!(keys(&background).is_empty());
    }

    #[test]
    fn rescheduling_replaces_the_fallback_and_stop_clears_wakeups() {
        let mut background = Background::default();
        run(
            &mut background,
            "ScheduleWakeup",
            "w1",
            json!({}),
            json!({"scheduledFor": 10_000}),
            1_000,
        );
        // The turn that ends the wakeup schedules the next one.
        run(
            &mut background,
            "ScheduleWakeup",
            "w2",
            json!({}),
            json!({"scheduledFor": 90_000}),
            10_500,
        );
        background.turn_ended(10_000, 11_000);
        assert_eq!(keys(&background), ["w2"]);
        // A stop call that failed leaves the wakeup and adds an unknown
        // schedule.
        background.observe(
            &call("ScheduleWakeup", "w3", &json!({"stop": true})),
            12_000,
            true,
        );
        assert_eq!(keys(&background), ["w2"]);
        background.observe(
            &json!({"type": "user", "message": {"content": [
                {"type": "tool_result", "tool_use_id": "w3", "is_error": true, "content": "no"}
            ]}}),
            12_000,
            true,
        );
        assert_eq!(keys(&background), ["w2", "w3"]);
        background.clear();
        run(
            &mut background,
            "ScheduleWakeup",
            "w4",
            json!({}),
            json!({"scheduledFor": 90_000}),
            1_000,
        );
        run(
            &mut background,
            "ScheduleWakeup",
            "w5",
            json!({"stop": true}),
            json!({"stopped": true}),
            2_000,
        );
        assert!(keys(&background).is_empty());
    }

    #[test]
    fn an_unreadable_scheduling_call_lasts_until_exit() {
        let mut background = Background::default();
        background.observe(&call("CronCreate", "c1", &json!({})), 1_000, true);
        background.observe(
            &json!({"type": "user", "message": {"content": [
                {"type": "tool_result", "tool_use_id": "c1", "content": "ok"}
            ]}}),
            1_000,
            true,
        );
        background.turn_ended(u64::MAX / 2, u64::MAX / 2 + 1);
        assert_eq!(keys(&background), ["c1"]);
        background.clear();
        assert!(keys(&background).is_empty());
    }

    #[test]
    fn a_resume_restores_unexpired_recurring_and_future_one_shot_jobs() {
        let day = 24 * 60 * 60 * 1000;
        let now = 100 * day;
        let stamp = |ms: u64| {
            DateTime::from_timestamp_millis(i64::try_from(ms).unwrap())
                .unwrap()
                .to_rfc3339()
        };
        let mut entries = Vec::new();
        let mut add = |name: &str, id: &str, input: Value, output: Value, at: u64| {
            let mut c = call(name, id, &input);
            c["timestamp"] = json!(stamp(at));
            let mut r = result(id, &output);
            r["toolUseResult"] = r["tool_use_result"].take();
            r["timestamp"] = json!(stamp(at));
            entries.extend([c, r]);
        };
        add(
            "CronCreate",
            "a",
            json!({}),
            json!({"id": "fresh", "recurring": true, "nextFireAt": now}),
            now - day,
        );
        add(
            "CronCreate",
            "b",
            json!({}),
            json!({"id": "old", "recurring": true, "nextFireAt": now}),
            now - 8 * day,
        );
        add(
            "CronCreate",
            "c",
            json!({}),
            json!({"id": "due", "recurring": false, "nextFireAt": now - 1}),
            now - day,
        );
        add(
            "CronCreate",
            "d",
            json!({}),
            json!({"id": "later", "recurring": false, "nextFireAt": now + day}),
            now - day,
        );
        add(
            "ScheduleWakeup",
            "e",
            json!({}),
            json!({"scheduledFor": now + day}),
            now - day,
        );
        add(
            "CronCreate",
            "f",
            json!({}),
            json!({"id": "gone", "recurring": true, "nextFireAt": now}),
            now - day,
        );
        add(
            "CronDelete",
            "g",
            json!({"id": "gone"}),
            json!({"id": "gone"}),
            now - day,
        );
        let mut background = Background::default();
        background.seed(&entries, now);
        assert_eq!(keys(&background), ["fresh", "later"]);
    }
}
