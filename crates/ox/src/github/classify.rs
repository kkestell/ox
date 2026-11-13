use serde::Serialize;

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Group {
    Fix,
    Replies,
    Merge,
    Review,
    Waiting,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Reason {
    DirectReview,
    UpdatedSinceReview,
    Replies,
    TeamReview,
    RequestedChanges,
    Draft,
    RequiredCheckFailed,
    Conflicts,
    Behind,
    ChangesRequested,
    Unanswered,
    WaitingForReview,
    ReadyToMerge,
    ChecksRunning,
    Blocked,
    WaitingForGithub,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct Classification {
    pub group: Group,
    pub reason: Reason,
    pub rank: u8,
    pub count: usize,
}

#[derive(Default)]
#[expect(
    clippy::struct_excessive_bools,
    reason = "these are independent GitHub facts"
)]
pub struct Facts<'a> {
    pub own: bool,
    pub draft: bool,
    pub required_failed: bool,
    pub merge: &'a str,
    pub review: &'a str,
    pub checks: &'a str,
    pub direct: bool,
    pub requested: bool,
    pub reviewed: &'a str,
    pub current_head_changes: bool,
    pub updated_since_review: bool,
    pub unanswered: usize,
    pub pending_reviews: u64,
}

pub fn classify(f: &Facts<'_>) -> Option<Classification> {
    use Group::{Fix, Merge, Replies as RepliesGroup, Review, Waiting};
    use Reason::{
        Behind, Blocked, ChangesRequested, ChecksRunning, Conflicts, DirectReview, Draft,
        ReadyToMerge, Replies, RequestedChanges, RequiredCheckFailed, TeamReview, Unanswered,
        UpdatedSinceReview, WaitingForGithub, WaitingForReview,
    };
    let (group, reason, rank) = if !f.own {
        if f.direct {
            (Review, DirectReview, 0)
        } else if f.reviewed == "CHANGES_REQUESTED" && f.updated_since_review {
            (RepliesGroup, UpdatedSinceReview, 1)
        } else if f.unanswered > 0 {
            (RepliesGroup, Replies, 2)
        } else if f.requested && f.reviewed.is_empty() {
            (Review, TeamReview, 9)
        } else if f.reviewed == "CHANGES_REQUESTED" {
            (Waiting, RequestedChanges, 1)
        } else {
            return None;
        }
    } else if f.draft {
        (Waiting, Draft, 0)
    } else if f.required_failed {
        (Fix, RequiredCheckFailed, 3)
    } else if f.merge == "DIRTY" {
        (Fix, Conflicts, 4)
    } else if f.merge == "BEHIND" {
        (Fix, Behind, 5)
    } else if f.review == "CHANGES_REQUESTED" && f.current_head_changes {
        (Fix, ChangesRequested, 6)
    } else if f.unanswered > 0 {
        (Fix, Unanswered, 7)
    } else if f.review == "CHANGES_REQUESTED" {
        (Waiting, WaitingForReview, 2)
    } else if f.pending_reviews > 0 && f.review != "APPROVED" {
        (Waiting, WaitingForReview, 3)
    } else if matches!(f.merge, "CLEAN" | "HAS_HOOKS" | "UNSTABLE") {
        (Merge, ReadyToMerge, 8)
    } else if matches!(f.checks, "PENDING" | "EXPECTED") {
        (Waiting, ChecksRunning, 4)
    } else if f.review == "REVIEW_REQUIRED" {
        (Waiting, WaitingForReview, 3)
    } else if f.merge == "BLOCKED" {
        (Waiting, Blocked, 5)
    } else {
        (Waiting, WaitingForGithub, 6)
    };
    Some(Classification {
        group,
        reason,
        rank,
        count: f.unanswered,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn precedence_and_waits() {
        let mut f = Facts {
            own: true,
            draft: true,
            required_failed: true,
            merge: "DIRTY",
            ..Facts::default()
        };
        assert_eq!(classify(&f).unwrap().reason, Reason::Draft);
        f.draft = false;
        assert_eq!(classify(&f).unwrap().reason, Reason::RequiredCheckFailed);
        f.required_failed = false;
        assert_eq!(classify(&f).unwrap().reason, Reason::Conflicts);
        f.merge = "CLEAN";
        f.pending_reviews = 1;
        assert_eq!(classify(&f).unwrap().reason, Reason::WaitingForReview);
        f.review = "APPROVED";
        assert_eq!(classify(&f).unwrap().reason, Reason::ReadyToMerge);
        f.own = false;
        f.direct = true;
        f.unanswered = 2;
        assert_eq!(classify(&f).unwrap().reason, Reason::DirectReview);
        f.direct = false;
        assert_eq!(classify(&f).unwrap().reason, Reason::Replies);
        f.unanswered = 0;
        f.reviewed = "CHANGES_REQUESTED";
        f.updated_since_review = true;
        assert_eq!(classify(&f).unwrap().reason, Reason::UpdatedSinceReview);
        f.updated_since_review = false;
        assert_eq!(classify(&f).unwrap().reason, Reason::RequestedChanges);
        f.reviewed = "";
        assert_eq!(classify(&f), None);
    }
}

#[cfg(test)]
mod reference_cases {
    use super::*;

    #[test]
    fn all_attention_branches() {
        use Reason::{
            Behind, Blocked, ChangesRequested, ChecksRunning, DirectReview, ReadyToMerge,
            RequestedChanges, TeamReview, Unanswered, WaitingForGithub, WaitingForReview,
        };
        let cases = [
            (
                Facts {
                    direct: true,
                    ..Facts::default()
                },
                Some(DirectReview),
            ),
            (
                Facts {
                    requested: true,
                    ..Facts::default()
                },
                Some(TeamReview),
            ),
            (
                Facts {
                    requested: true,
                    reviewed: "APPROVED",
                    ..Facts::default()
                },
                None,
            ),
            (
                Facts {
                    reviewed: "CHANGES_REQUESTED",
                    ..Facts::default()
                },
                Some(RequestedChanges),
            ),
            (
                Facts {
                    own: true,
                    merge: "BEHIND",
                    ..Facts::default()
                },
                Some(Behind),
            ),
            (
                Facts {
                    own: true,
                    review: "CHANGES_REQUESTED",
                    current_head_changes: true,
                    unanswered: 1,
                    ..Facts::default()
                },
                Some(ChangesRequested),
            ),
            (
                Facts {
                    own: true,
                    review: "CHANGES_REQUESTED",
                    unanswered: 1,
                    ..Facts::default()
                },
                Some(Unanswered),
            ),
            (
                Facts {
                    own: true,
                    review: "CHANGES_REQUESTED",
                    ..Facts::default()
                },
                Some(WaitingForReview),
            ),
            (
                Facts {
                    own: true,
                    merge: "UNSTABLE",
                    ..Facts::default()
                },
                Some(ReadyToMerge),
            ),
            (
                Facts {
                    own: true,
                    checks: "PENDING",
                    review: "REVIEW_REQUIRED",
                    merge: "BLOCKED",
                    ..Facts::default()
                },
                Some(ChecksRunning),
            ),
            (
                Facts {
                    own: true,
                    review: "REVIEW_REQUIRED",
                    merge: "BLOCKED",
                    ..Facts::default()
                },
                Some(WaitingForReview),
            ),
            (
                Facts {
                    own: true,
                    merge: "BLOCKED",
                    ..Facts::default()
                },
                Some(Blocked),
            ),
            (
                Facts {
                    own: true,
                    merge: "UNKNOWN",
                    ..Facts::default()
                },
                Some(WaitingForGithub),
            ),
        ];
        for (facts, expected) in cases {
            assert_eq!(classify(&facts).map(|decision| decision.reason), expected);
        }
    }
}
