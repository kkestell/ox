use super::*;
use serde_json::json;
use std::os::unix::fs::PermissionsExt;

fn pull() -> serde_json::Value {
    json!({"id":"P1","number":1,"title":"A","url":"https://github.com/o/r/pull/1","updatedAt":"2026-01-01T00:00:00Z","isDraft":false,"headRefName":"topic","headRefOid":"head","repository":{"nameWithOwner":"o/r"},"author":{"login":"alice"},"reviewDecision":null,"mergeStateStatus":"CLEAN","statusCheckRollup":null,"reviewRequests":{"totalCount":0},"latestOpinionatedReviews":{"nodes":[]},"reviews":{"nodes":[]},"resolved":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]},"commits":{"nodes":[{"commit":{"committedDate":"2026-01-01T00:00:00Z"}}]}})
}
fn conversation() -> serde_json::Value {
    json!({"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]},"comments":{"nodes":[]}})
}
#[test]
fn comments_and_reviews_follow_account_and_commit() {
    let mut p = pull();
    p["reviewDecision"] = json!("CHANGES_REQUESTED");
    p["latestOpinionatedReviews"]["nodes"] =
        json!([{"author":{"login":"bob"},"state":"CHANGES_REQUESTED","commit":{"oid":"old"}}]);
    p["reviews"]["nodes"] = json!([{"submittedAt":"2026-01-02T00:00:00Z","commit":{"oid":"old"}}]);
    let p: Pull = serde_json::from_value(p).unwrap();
    let mut c = conversation();
    c["comments"]["nodes"] = json!([
            {"author":{"__typename":"User","login":"bob"},"createdAt":"2026-01-03T00:00:00Z"},
            {"author":{"__typename":"Bot","login":"bot"},"createdAt":"2026-01-04T00:00:00Z"}]);
    let c: Conversation = serde_json::from_value(c).unwrap();
    let own = facts(&p, &c, "alice", false, false, false);
    assert!(!own.current_head_changes);
    assert_eq!(own.unanswered, 1);
    let reviewer = facts(&p, &c, "bob", false, false, false);
    assert!(reviewer.updated_since_review);
    assert_eq!(reviewer.reviewed, "CHANGES_REQUESTED");
    assert_eq!(
        classify(&reviewer).unwrap().reason,
        classify::Reason::UpdatedSinceReview
    );
}

#[tokio::test]
async fn refresh_reuses_conversations_and_retains_failed_accounts() {
    let dir = tempfile::tempdir().unwrap();
    let exe = dir.path().join("gh");
    let fixture = format!(
        r#"#!/bin/sh
D='{}'
case "$1 $2" in
 'auth status') [ ! -f "$D/discovery-fail" ] || exit 9; /bin/cat "$D/accounts" ;;
 'auth token') [ ! -f "$D/fail-$6" ] || {{ /bin/echo failed >&2; exit 1; }}; /bin/echo "token-$6" ;;
 'api graphql')
 case "$4" in
  *'fragment pr on PullRequest'*) /bin/cat "$D/search" ;;
  *'fragment thread on PullRequestReviewThread'*) /bin/echo x >> "$D/conversations"; /bin/cat "$D/conversation" ;;
  *'resolved:reviewThreads'*) /bin/cat "$D/resolved" ;;
  *) exit 4 ;;
 esac ;;
 *) exit 3 ;;
esac
"#,
        dir.path().display()
    );
    std::fs::write(&exe, fixture).unwrap();
    std::fs::set_permissions(&exe, std::fs::Permissions::from_mode(0o755)).unwrap();
    let write = |name: &str, value: serde_json::Value| {
        std::fs::write(dir.path().join(name), value.to_string()).unwrap();
    };
    write(
        "accounts",
        json!({"hosts":{"github.com":[{"login":"alice"},{"login":"bob"}]}}),
    );
    let mut initial = pull();
    initial["resolved"] = json!({"pageInfo":{"hasNextPage":true,"endCursor":"next"},"nodes":[]});
    write(
        "search",
        json!({"data":{"search":{"issueCount":1,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[initial]}}}),
    );
    let resolution = |value| json!({"data":{"node":{"resolved":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[{"isResolved":value}]}}}});
    write("resolved", resolution(false));
    write("conversation", json!({"data":{"p0":conversation()}}));
    let mut feed = Feed {
        gh: Gh::with_path(dir.path().as_os_str()),
        ..Feed::default()
    };
    let first = feed.refresh().await;
    assert_eq!(first.accounts.len(), 2);
    assert_eq!(first.accounts[0].pull_requests.len(), 1);
    assert_eq!(
        first.accounts[1].pull_requests[0].classification.reason,
        classify::Reason::DirectReview
    );
    assert_eq!(
        std::fs::read_to_string(dir.path().join("conversations"))
            .unwrap()
            .lines()
            .count(),
        2
    );
    let second = feed.refresh().await;
    assert!(second.accounts.iter().all(|a| a.error.is_none()));
    assert_eq!(
        std::fs::read_to_string(dir.path().join("conversations"))
            .unwrap()
            .lines()
            .count(),
        2
    );
    write("resolved", resolution(true));
    feed.refresh().await;
    assert_eq!(
        std::fs::read_to_string(dir.path().join("conversations"))
            .unwrap()
            .lines()
            .count(),
        4
    );
    let mut updated = pull();
    updated["updatedAt"] = json!("2026-01-05T00:00:00Z");
    updated["resolved"] = json!({"pageInfo":{"hasNextPage":true,"endCursor":"next"},"nodes":[]});
    write(
        "search",
        json!({"data":{"search":{"issueCount":1,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[updated]}}}),
    );
    let changed = feed.refresh().await;
    assert_eq!(
        std::fs::read_to_string(dir.path().join("conversations"))
            .unwrap()
            .lines()
            .count(),
        6
    );
    std::fs::write(dir.path().join("discovery-fail"), "").unwrap();
    let discovery = feed.refresh().await;
    assert!(discovery.error.is_some());
    assert_eq!(discovery.accounts.len(), 2);
    std::fs::remove_file(dir.path().join("discovery-fail")).unwrap();
    std::fs::write(dir.path().join("fail-alice"), "").unwrap();
    let third = feed.refresh().await;
    assert!(third.accounts[0].error.is_some());
    assert_eq!(
        third.accounts[0].last_success,
        changed.accounts[0].last_success
    );
    assert_eq!(third.accounts[0].pull_requests.len(), 1);
    assert!(third.accounts[1].error.is_none());
    write(
        "accounts",
        json!({"hosts":{"github.com":[{"login":"bob"}]}}),
    );
    let fourth = feed.refresh().await;
    assert_eq!(fourth.accounts.len(), 1);
    assert_eq!(fourth.accounts[0].login, "bob");
}
#[tokio::test]
async fn pagination_discovers_requested_pulls_and_checks_later_pages() {
    let dir = tempfile::tempdir().unwrap();
    let exe = dir.path().join("gh");
    let script = format!(
        r#"#!/bin/sh
D='{}'
case "$*" in
  *'fragment pr on PullRequest'*'involves:@me'*'after=next'*) /bin/cat "$D/involved2" ;;
  *'fragment pr on PullRequest'*'involves:@me'*) /bin/cat "$D/involved1" ;;
  *'fragment pr on PullRequest'*'user-review-requested'*) /bin/cat "$D/direct" ;;
  *'fragment pr on PullRequest'*) /bin/cat "$D/requested" ;;
  *'resolved:reviewThreads'*) /bin/cat "$D/resolved" ;;
  *'contexts(first:100'*'after=check-next'*) /bin/cat "$D/check2" ;;
  *'contexts(first:100'*) /bin/cat "$D/check1" ;;
  *) exit 1 ;;
esac
"#,
        dir.path().display()
    );
    std::fs::write(&exe, script).unwrap();
    std::fs::set_permissions(&exe, std::fs::Permissions::from_mode(0o755)).unwrap();
    let write = |name: &str, value: serde_json::Value| {
        std::fs::write(dir.path().join(name), value.to_string()).unwrap();
    };
    let page = |nodes: Vec<serde_json::Value>, next: bool, cursor: Option<&str>| json!({"data":{"search":{"issueCount":2,"pageInfo":{"hasNextPage":next,"endCursor":cursor},"nodes":nodes}}});
    let mut first = pull();
    first["resolved"] = json!({"pageInfo":{"hasNextPage":true,"endCursor":"thread-next"},"nodes":[{"isResolved":false}]});
    let mut second = pull();
    second["id"] = json!("P2");
    second["url"] = json!("https://github.com/o/r/pull/2");
    second["number"] = json!(2);
    write("involved1", page(vec![first], true, Some("next")));
    write("involved2", page(vec![], false, None));
    write("requested", page(vec![second.clone()], false, None));
    write("direct", page(vec![second], false, None));
    write(
        "resolved",
        json!({"data":{"node":{"resolved":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[{"isResolved":true}]}}}}),
    );
    let checks = |nodes: serde_json::Value, next: bool, cursor: Option<&str>| json!({"data":{"repository":{"pullRequest":{"statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":next,"endCursor":cursor},"nodes":nodes}}}}}});
    write(
        "check1",
        checks(
            json!([{"isRequired":false,"conclusion":"FAILURE"}]),
            true,
            Some("check-next"),
        ),
    );
    write(
        "check2",
        checks(json!([{"isRequired":true,"state":"ERROR"}]), false, None),
    );
    let gh = Gh::with_path(dir.path().as_os_str());
    let listing = query::listing(&gh, "token", "alice").await.unwrap();
    assert_eq!(listing.pulls.len(), 2);
    assert!(listing.requested.contains("https://github.com/o/r/pull/2"));
    assert!(listing.direct.contains("https://github.com/o/r/pull/2"));
    assert_eq!(stamp(&listing.pulls[0]), "2026-01-01T00:00:00Zur");
    assert!(
        query::required_failed(&gh, "token", &listing.pulls[0])
            .await
            .unwrap()
    );
    write(
        "check2",
        checks(json!([{"isRequired":false,"state":"ERROR"}]), false, None),
    );
    assert!(
        !query::required_failed(&gh, "token", &listing.pulls[0])
            .await
            .unwrap()
    );
}
#[tokio::test]
async fn search_ceiling_is_reported() {
    let dir = tempfile::tempdir().unwrap();
    let exe = dir.path().join("gh");
    std::fs::write(
        &exe,
        format!("#!/bin/sh\n/bin/cat '{}'/page\n", dir.path().display()),
    )
    .unwrap();
    std::fs::set_permissions(&exe, std::fs::Permissions::from_mode(0o755)).unwrap();
    let page = json!({"data":{"search":{"issueCount":1200,"pageInfo":{"hasNextPage":true,"endCursor":"next"},"nodes":vec![pull(); 100]}}});
    std::fs::write(dir.path().join("page"), page.to_string()).unwrap();
    let gh = Gh::with_path(dir.path().as_os_str());
    let listing = query::listing(&gh, "token", "alice").await.unwrap();
    assert!(listing.truncated);
    assert_eq!(listing.pulls.len(), 1);
}

#[tokio::test]
async fn reads_review_threads_after_the_first_page() {
    let dir = tempfile::tempdir().unwrap();
    let exe = dir.path().join("gh");
    let script = format!(
        r#"#!/bin/sh
case "$*" in
 *'after=next'*) /bin/cat '{}/next' ;;
 *) /bin/cat '{}/first' ;;
esac
"#,
        dir.path().display(),
        dir.path().display()
    );
    std::fs::write(&exe, script).unwrap();
    std::fs::set_permissions(&exe, std::fs::Permissions::from_mode(0o755)).unwrap();
    let mut first = conversation();
    first["reviewThreads"]["pageInfo"] = json!({"hasNextPage":true,"endCursor":"next"});
    let thread = json!({"isResolved":false,"first":{"nodes":[]},"last":{"nodes":[]}});
    std::fs::write(
        dir.path().join("first"),
        json!({"data":{"p0":first}}).to_string(),
    )
    .unwrap();
    std::fs::write(dir.path().join("next"), json!({"data":{"node":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[thread]}}}}).to_string()).unwrap();
    let gh = Gh::with_path(dir.path().as_os_str());
    let p: Pull = serde_json::from_value(pull()).unwrap();
    let conversations = query::conversations(&gh, "token", &[&p]).await.unwrap();
    assert_eq!(conversations["P1"].review_threads.nodes.len(), 1);
}

#[test]
fn pagination_metadata_is_required() {
    assert!(serde_json::from_value::<query::Page<query::Resolution>>(json!({"nodes":[]})).is_err());
}
