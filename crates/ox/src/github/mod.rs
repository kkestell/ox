mod classify;
mod query;

use classify::{Classification, Facts, classify};
use ox_github::Gh;
use query::{Conversation, Pull};
use serde::Serialize;
use std::collections::HashMap;
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Clone, Default, Serialize)]
pub struct Snapshot {
    pub error: Option<String>,
    pub accounts: Vec<Account>,
}
#[derive(Clone, Default, Serialize)]
pub struct Account {
    pub login: String,
    pub last_success: Option<u64>,
    pub error: Option<String>,
    pub truncated: bool,
    pub pull_requests: Vec<Row>,
}
#[derive(Clone, Serialize)]
pub struct Row {
    pub repository: String,
    pub number: u64,
    pub url: String,
    pub title: String,
    pub author: String,
    pub head_branch: String,
    pub updated_at: String,
    pub classification: Classification,
}
#[derive(Clone)]
struct Heard {
    stamp: String,
    conversation: Conversation,
}
#[derive(Default)]
pub struct Feed {
    snapshot: Snapshot,
    conversations: HashMap<String, HashMap<String, Heard>>,
    gh: Gh,
}

impl Feed {
    pub async fn refresh(&mut self) -> Snapshot {
        let logins = match self.gh.accounts().await {
            Ok(logins) => logins,
            Err(error) => {
                self.snapshot.error = Some(error);
                return self.snapshot.clone();
            }
        };
        self.snapshot.error = None;
        let mut previous: HashMap<_, _> = self
            .snapshot
            .accounts
            .clone()
            .into_iter()
            .map(|a| (a.login.clone(), a))
            .collect();
        self.conversations.retain(|login, _| logins.contains(login));
        let mut accounts = Vec::new();
        for login in logins {
            let mut account = previous.remove(&login).unwrap_or_else(|| Account {
                login: login.clone(),
                ..Account::default()
            });
            match self.read_account(&login).await {
                Ok((rows, cache, truncated)) => {
                    account.pull_requests = rows;
                    account.truncated = truncated;
                    account.last_success = Some(
                        SystemTime::now()
                            .duration_since(UNIX_EPOCH)
                            .unwrap_or_default()
                            .as_millis()
                            .try_into()
                            .unwrap_or(u64::MAX),
                    );
                    account.error = None;
                    self.conversations.insert(login, cache);
                }
                Err(error) => account.error = Some(error),
            }
            accounts.push(account);
        }
        self.snapshot.accounts = accounts;
        self.snapshot.clone()
    }

    async fn read_account(
        &self,
        login: &str,
    ) -> Result<(Vec<Row>, HashMap<String, Heard>, bool), String> {
        let token = self.gh.token(login).await?;
        let listing = query::listing(&self.gh, &token, login).await?;
        let old = self.conversations.get(login);
        let mut cache = HashMap::new();
        let mut stale = Vec::new();
        for pull in &listing.pulls {
            let stamp = stamp(pull);
            if let Some(heard) = old
                .and_then(|old| old.get(&pull.id))
                .filter(|heard| heard.stamp == stamp)
            {
                cache.insert(pull.id.clone(), heard.clone());
            } else {
                stale.push(pull);
            }
        }
        let fresh = query::conversations(&self.gh, &token, &stale).await?;
        for pull in stale {
            let conversation = fresh.get(&pull.id).ok_or("missing conversation")?.clone();
            cache.insert(
                pull.id.clone(),
                Heard {
                    stamp: stamp(pull),
                    conversation,
                },
            );
        }
        let mut rows = Vec::new();
        for pull in &listing.pulls {
            let conversation = &cache
                .get(&pull.id)
                .ok_or("missing cached conversation")?
                .conversation;
            let author = pull.author.as_ref().map_or("", |a| a.login.as_str());
            let own = author == login;
            let checks = pull
                .status_check_rollup
                .as_ref()
                .map_or("", |c| c.state.as_str());
            let required_failed = if own && matches!(checks, "FAILURE" | "ERROR") {
                query::required_failed(&self.gh, &token, pull).await?
            } else {
                false
            };
            let facts = facts(
                pull,
                conversation,
                login,
                listing.requested.contains(&pull.url),
                listing.direct.contains(&pull.url),
                required_failed,
            );
            if let Some(classification) = classify(&facts) {
                rows.push(Row {
                    repository: pull.repository.name_with_owner.clone(),
                    number: pull.number,
                    url: pull.url.clone(),
                    title: pull.title.clone(),
                    author: author.to_owned(),
                    head_branch: pull.head_ref_name.clone(),
                    updated_at: pull.updated_at.clone(),
                    classification,
                });
            }
        }
        rows.sort_by(|a, b| {
            a.classification
                .rank
                .cmp(&b.classification.rank)
                .then_with(|| b.updated_at.cmp(&a.updated_at))
        });
        Ok((rows, cache, listing.truncated))
    }
}

fn stamp(pull: &Pull) -> String {
    let mut stamp = pull.updated_at.clone();
    for thread in &pull.resolved.nodes {
        stamp.push(if thread.is_resolved { 'r' } else { 'u' });
    }
    stamp
}

fn facts<'a>(
    pull: &'a Pull,
    conversation: &Conversation,
    login: &str,
    requested: bool,
    direct: bool,
    required_failed: bool,
) -> Facts<'a> {
    let author = pull.author.as_ref().map_or("", |a| a.login.as_str());
    let own = author == login;
    let reviewed = pull
        .latest_opinionated_reviews
        .nodes
        .iter()
        .find(|r| r.author.as_ref().is_some_and(|a| a.login == login))
        .map_or("", |r| r.state.as_str());
    let current_head_changes = pull.latest_opinionated_reviews.nodes.iter().any(|r| {
        r.state == "CHANGES_REQUESTED"
            && r.commit
                .as_ref()
                .is_some_and(|c| c.oid == pull.head_ref_oid)
    });
    let updated_since_review = pull.reviews.nodes.first().is_some_and(|review| {
        review
            .commit
            .as_ref()
            .is_none_or(|c| c.oid != pull.head_ref_oid)
            || conversation
                .comments
                .nodes
                .iter()
                .chain(
                    conversation
                        .review_threads
                        .nodes
                        .iter()
                        .flat_map(|t| &t.last.nodes),
                )
                .any(|c| {
                    !author.is_empty() && c.person() == author && c.created_at > review.submitted_at
                })
    });
    let after_commit = |c: &query::Comment| {
        pull.commits
            .nodes
            .first()
            .is_none_or(|n| c.created_at > n.commit.committed_date)
    };
    let answered = own
        && pull.review_decision.as_deref() == Some("CHANGES_REQUESTED")
        && !current_head_changes;
    let mut unanswered = conversation
        .review_threads
        .nodes
        .iter()
        .filter(|t| {
            let first = t.first.nodes.last().map_or("", query::Comment::person);
            t.last.nodes.last().is_some_and(|last| {
                !t.is_resolved
                    && !last.person().is_empty()
                    && last.person() != login
                    && (own || first == login)
                    && (!answered || after_commit(last))
            })
        })
        .count();
    if own
        && let Some(comment) = conversation
            .comments
            .nodes
            .iter()
            .rev()
            .find(|c| !c.person().is_empty())
        && comment.person() != login
        && after_commit(comment)
    {
        unanswered += 1;
    }
    Facts {
        own,
        draft: pull.is_draft,
        required_failed,
        merge: &pull.merge_state_status,
        review: pull.review_decision.as_deref().unwrap_or(""),
        checks: pull
            .status_check_rollup
            .as_ref()
            .map_or("", |c| c.state.as_str()),
        direct,
        requested,
        reviewed,
        current_head_changes,
        updated_since_review,
        unanswered,
        pending_reviews: pull.review_requests.total_count,
    }
}

#[cfg(test)]
mod tests;
