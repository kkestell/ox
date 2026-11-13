use ox_github::Gh;
use serde::Deserialize;
use serde_json::{Value, json};
use std::collections::{HashMap, HashSet};
use std::fmt::Write;

const PR: &str = r"fragment pr on PullRequest {
 id number title url updatedAt isDraft headRefName headRefOid repository{nameWithOwner}
 author{login} reviewDecision mergeStateStatus statusCheckRollup{state} reviewRequests{totalCount}
 latestOpinionatedReviews(first:10){nodes{author{login} state commit{oid}}}
 reviews(last:1,author:$login,states:[APPROVED,CHANGES_REQUESTED,COMMENTED,DISMISSED]){nodes{submittedAt commit{oid}}}
 resolved:reviewThreads(first:100){pageInfo{hasNextPage endCursor} nodes{isResolved}}
 commits(last:1){nodes{commit{committedDate}}}
}";
const THREAD: &str = "fragment thread on PullRequestReviewThread { isResolved first:comments(first:1){nodes{author{__typename login} createdAt}} last:comments(last:1){nodes{author{__typename login} createdAt}} }";

#[derive(Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PageInfo {
    pub has_next_page: bool,
    pub end_cursor: Option<String>,
}
#[derive(Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Page<T> {
    pub nodes: Vec<T>,
    pub page_info: PageInfo,
}
#[derive(Clone, Default, Deserialize)]
pub struct Nodes<T> {
    pub nodes: Vec<T>,
}
#[derive(Clone, Deserialize)]
pub struct Person {
    pub login: String,
    #[serde(rename = "__typename", default)]
    pub kind: String,
}
#[derive(Clone, Deserialize)]
pub struct Commit {
    pub oid: String,
}
#[derive(Clone, Deserialize)]
pub struct Opinion {
    pub author: Option<Person>,
    pub state: String,
    pub commit: Option<Commit>,
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Review {
    pub submitted_at: String,
    pub commit: Option<Commit>,
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Comment {
    pub author: Option<Person>,
    pub created_at: String,
}
impl Comment {
    pub fn person(&self) -> &str {
        self.author
            .as_ref()
            .filter(|a| a.kind == "User")
            .map_or("", |a| a.login.as_str())
    }
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Thread {
    pub is_resolved: bool,
    pub first: Nodes<Comment>,
    pub last: Nodes<Comment>,
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Resolution {
    pub is_resolved: bool,
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Conversation {
    pub review_threads: Page<Thread>,
    pub comments: Nodes<Comment>,
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Repository {
    pub name_with_owner: String,
}
#[derive(Clone, Deserialize)]
pub struct Rollup {
    pub state: String,
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Count {
    pub total_count: u64,
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DatedCommit {
    pub committed_date: String,
}
#[derive(Clone, Deserialize)]
pub struct CommitNode {
    pub commit: DatedCommit,
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Pull {
    pub id: String,
    pub number: u64,
    pub title: String,
    pub url: String,
    pub updated_at: String,
    pub is_draft: bool,
    pub head_ref_name: String,
    pub head_ref_oid: String,
    pub repository: Repository,
    pub author: Option<Person>,
    pub review_decision: Option<String>,
    pub merge_state_status: String,
    pub status_check_rollup: Option<Rollup>,
    pub review_requests: Count,
    pub latest_opinionated_reviews: Nodes<Opinion>,
    pub reviews: Nodes<Review>,
    pub resolved: Page<Resolution>,
    pub commits: Nodes<CommitNode>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Search {
    nodes: Vec<Value>,
    page_info: PageInfo,
    issue_count: u64,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Check {
    is_required: bool,
    conclusion: Option<String>,
    state: Option<String>,
}
#[derive(Deserialize)]
struct Contexts {
    contexts: Page<Check>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct CheckPull {
    status_check_rollup: Option<Contexts>,
}

async fn read<T: serde::de::DeserializeOwned>(
    gh: &Gh,
    token: &str,
    query: &str,
    vars: Value,
    field: &str,
) -> Result<T, String> {
    let mut data = gh.graphql(token, query, &vars).await?;
    serde_json::from_value(
        data.get_mut(field)
            .ok_or_else(|| format!("missing {field}"))?
            .take(),
    )
    .map_err(|e| format!("invalid {field}: {e}"))
}
fn cursor(page: &PageInfo) -> Result<Option<String>, String> {
    if page.has_next_page {
        Ok(Some(
            page.end_cursor.clone().ok_or("missing pagination cursor")?,
        ))
    } else {
        Ok(None)
    }
}

pub struct Listing {
    pub pulls: Vec<Pull>,
    pub requested: HashSet<String>,
    pub direct: HashSet<String>,
    pub truncated: bool,
}

pub async fn listing(gh: &Gh, token: &str, login: &str) -> Result<Listing, String> {
    let mut pulls = Vec::new();
    let mut seen = HashSet::new();
    let mut requested = HashSet::new();
    let mut direct = HashSet::new();
    let mut truncated = false;
    for (index, search) in [
        "(involves:@me OR review-requested:@me OR reviewed-by:@me)",
        "review-requested:@me",
        "user-review-requested:@me",
    ]
    .iter()
    .enumerate()
    {
        let mut after = None;
        let mut count = 0;
        loop {
            let search_type = if index == 0 {
                "ISSUE_ADVANCED"
            } else {
                "ISSUE"
            };
            let query = format!(
                "{PR} query($login:String!,$search:String!,$after:String){{ search(query:$search,type:{search_type},first:100,after:$after){{issueCount pageInfo{{hasNextPage endCursor}} nodes{{...pr}}}} }}"
            );
            let mut vars =
                json!({"login":login,"search":format!("is:pr is:open sort:updated-desc {search}")});
            if let Some(value) = &after {
                vars["after"] = json!(value);
            }
            let page: Search = read(gh, token, &query, vars, "search").await?;
            count += page.nodes.len();
            truncated |= page.issue_count > 1000;
            for node in page.nodes {
                if node.get("id").is_none() {
                    continue;
                }
                let pull: Pull = serde_json::from_value(node)
                    .map_err(|e| format!("invalid pull request: {e}"))?;
                if index == 1 {
                    requested.insert(pull.url.clone());
                }
                if index == 2 {
                    direct.insert(pull.url.clone());
                }
                if index != 2 && seen.insert(pull.id.clone()) {
                    pulls.push(pull);
                }
            }
            let next = cursor(&page.page_info)?;
            if count >= 1000 {
                truncated |= next.is_some() || page.issue_count > 1000;
                break;
            }
            if next.is_none() {
                break;
            }
            after = next;
        }
    }
    for pull in &mut pulls {
        let mut next = cursor(&pull.resolved.page_info)?;
        while let Some(after) = next {
            #[derive(Deserialize)]
            struct Node {
                resolved: Page<Resolution>,
            }
            let q = "query($id:ID!,$after:String!){node(id:$id){... on PullRequest{resolved:reviewThreads(first:100,after:$after){pageInfo{hasNextPage endCursor} nodes{isResolved}}}}}";
            let node: Node =
                read(gh, token, q, json!({"id":pull.id,"after":after}), "node").await?;
            next = cursor(&node.resolved.page_info)?;
            pull.resolved.nodes.extend(node.resolved.nodes);
        }
    }
    Ok(Listing {
        pulls,
        requested,
        direct,
        truncated,
    })
}

pub async fn conversations(
    gh: &Gh,
    token: &str,
    pulls: &[&Pull],
) -> Result<HashMap<String, Conversation>, String> {
    let mut result = HashMap::new();
    for batch in pulls.chunks(50) {
        let mut vars = serde_json::Map::new();
        let mut params = Vec::new();
        let mut fields = String::new();
        for (i, pull) in batch.iter().enumerate() {
            vars.insert(format!("id{i}"), json!(pull.id));
            params.push(format!("$id{i}:ID!"));
            write!(fields, "p{i}:node(id:$id{i}){{... on PullRequest{{reviewThreads(first:20){{pageInfo{{hasNextPage endCursor}} nodes{{...thread}}}} comments(last:20){{nodes{{author{{__typename login}} createdAt}}}}}}}}").expect("writing to a string");
        }
        let query = format!("{THREAD} query({}){{{fields}}}", params.join(","));
        let data = gh.graphql(token, &query, &Value::Object(vars)).await?;
        for (i, pull) in batch.iter().enumerate() {
            let mut conversation: Conversation = serde_json::from_value(
                data.get(format!("p{i}"))
                    .cloned()
                    .ok_or("missing conversation")?,
            )
            .map_err(|e| format!("invalid conversation: {e}"))?;
            let mut next = cursor(&conversation.review_threads.page_info)?;
            while let Some(after) = next {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase")]
                struct Node {
                    review_threads: Page<Thread>,
                }
                let q = format!(
                    "{THREAD} query($id:ID!,$after:String!){{node(id:$id){{... on PullRequest{{reviewThreads(first:100,after:$after){{pageInfo{{hasNextPage endCursor}} nodes{{...thread}}}}}}}}}}"
                );
                let node: Node =
                    read(gh, token, &q, json!({"id":pull.id,"after":after}), "node").await?;
                next = cursor(&node.review_threads.page_info)?;
                conversation
                    .review_threads
                    .nodes
                    .extend(node.review_threads.nodes);
            }
            result.insert(pull.id.clone(), conversation);
        }
    }
    Ok(result)
}

pub async fn required_failed(gh: &Gh, token: &str, pull: &Pull) -> Result<bool, String> {
    let (owner, name) = pull
        .repository
        .name_with_owner
        .split_once('/')
        .ok_or("invalid repository name")?;
    let q = format!(
        "query($owner:String!,$name:String!,$after:String){{repository(owner:$owner,name:$name){{pullRequest(number:{}){{statusCheckRollup{{contexts(first:100,after:$after){{pageInfo{{hasNextPage endCursor}} nodes{{... on CheckRun{{conclusion isRequired(pullRequestNumber:{})}} ... on StatusContext{{state isRequired(pullRequestNumber:{})}}}}}}}}}}}}}}",
        pull.number, pull.number, pull.number
    );
    let mut after = None;
    loop {
        let mut vars = json!({"owner":owner,"name":name});
        if let Some(value) = &after {
            vars["after"] = json!(value);
        }
        let data = gh.graphql(token, &q, &vars).await?;
        let pr: CheckPull = serde_json::from_value(
            data.pointer("/repository/pullRequest")
                .cloned()
                .ok_or("missing check pull request")?,
        )
        .map_err(|e| format!("invalid check response: {e}"))?;
        let Some(rollup) = pr.status_check_rollup else {
            return Ok(false);
        };
        if rollup.contexts.nodes.iter().any(|c| {
            c.is_required
                && (matches!(
                    c.conclusion.as_deref(),
                    Some(
                        "FAILURE"
                            | "TIMED_OUT"
                            | "CANCELLED"
                            | "ACTION_REQUIRED"
                            | "STARTUP_FAILURE"
                    )
                ) || matches!(c.state.as_deref(), Some("FAILURE" | "ERROR")))
        }) {
            return Ok(true);
        }
        after = cursor(&rollup.contexts.page_info)?;
        if after.is_none() {
            return Ok(false);
        }
    }
}
