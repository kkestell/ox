//! Account-scoped access to the GitHub CLI.

use std::ffi::OsString;

use serde_json::Value;
use tokio::process::Command;

/// A GitHub CLI executable and its operation-local search path.
#[derive(Clone, Debug, Default)]
pub struct Gh {
    path: Option<OsString>,
}

impl Gh {
    /// Use the supplied PATH for CLI children.
    pub fn with_path(path: impl Into<OsString>) -> Self {
        Self {
            path: Some(path.into()),
        }
    }

    fn command(&self) -> Command {
        let mut command = Command::new("gh");
        command
            .kill_on_drop(true)
            .env_remove("GH_TOKEN")
            .env_remove("GITHUB_TOKEN");
        if let Some(path) = &self.path {
            command.env("PATH", path);
        }
        command
    }

    async fn output(&self, mut command: Command) -> Result<Vec<u8>, String> {
        let output = command
            .output()
            .await
            .map_err(|error| format!("could not run gh: {error}"))?;
        if !output.status.success() {
            return Err(format!(
                "gh failed: {}",
                String::from_utf8_lossy(&output.stderr).trim()
            ));
        }
        Ok(output.stdout)
    }

    /// List the accounts known to gh on github.com.
    pub async fn accounts(&self) -> Result<Vec<String>, String> {
        let mut command = self.command();
        command.args([
            "auth",
            "status",
            "--hostname",
            "github.com",
            "--json",
            "hosts",
        ]);
        let data: Value = serde_json::from_slice(&self.output(command).await?)
            .map_err(|error| format!("invalid gh auth status: {error}"))?;
        let hosts = data
            .get("hosts")
            .and_then(Value::as_object)
            .ok_or("invalid gh auth status: missing hosts")?;
        let Some(accounts) = hosts.get("github.com") else {
            return Ok(Vec::new());
        };
        let accounts = accounts
            .as_array()
            .ok_or("invalid gh auth status: invalid accounts")?;
        let mut logins = accounts
            .iter()
            .map(|account| {
                account
                    .get("login")
                    .and_then(Value::as_str)
                    .map(str::to_owned)
                    .ok_or("invalid gh auth status: missing login")
            })
            .collect::<Result<Vec<_>, _>>()?;
        logins.sort();
        logins.dedup();
        Ok(logins)
    }

    /// Resolve a fresh token without changing the active account.
    pub async fn token(&self, login: &str) -> Result<String, String> {
        let mut command = self.command();
        command.args(["auth", "token", "--hostname", "github.com", "--user", login]);
        let bytes = self.output(command).await?;
        let token = String::from_utf8(bytes).map_err(|_| "gh returned a non-UTF-8 token")?;
        let token = token.trim().to_owned();
        if token.is_empty() {
            return Err("gh returned an empty token".into());
        }
        Ok(token)
    }

    /// Execute a GraphQL query with one account's token.
    pub async fn graphql(
        &self,
        token: &str,
        query: &str,
        variables: &Value,
    ) -> Result<Value, String> {
        let mut command = self.command();
        command.env("GH_TOKEN", token).args([
            "api",
            "graphql",
            "-f",
            &format!("query={query}"),
            "--hostname",
            "github.com",
        ]);
        let variables = variables
            .as_object()
            .ok_or("GraphQL variables must be an object")?;
        for (key, value) in variables {
            let value = match value {
                Value::String(value) => value.clone(),
                _ => value.to_string(),
            };
            command.arg("-f").arg(format!("{key}={value}"));
        }
        let output = self
            .output(command)
            .await
            .map_err(|error| error.replace(token, "[redacted]"))?;
        let response: Value = serde_json::from_slice(&output)
            .map_err(|error| format!("invalid GraphQL response: {error}"))?;
        if let Some(errors) = response
            .get("errors")
            .and_then(Value::as_array)
            .filter(|errors| !errors.is_empty())
        {
            return Err(format!(
                "GraphQL errors: {}",
                Value::Array(errors.clone())
                    .to_string()
                    .replace(token, "[redacted]")
            ));
        }
        response
            .get("data")
            .filter(|data| !data.is_null())
            .cloned()
            .ok_or_else(|| "GraphQL response has no data".into())
    }
}

#[cfg(test)]
mod tests {
    use std::os::unix::fs::PermissionsExt;

    use super::Gh;

    #[tokio::test]
    async fn account_access_ignores_ambient_tokens_and_scopes_graphql() {
        let dir = tempfile::tempdir().unwrap();
        let script = dir.path().join("gh");
        std::fs::write(
            &script,
            r#"#!/bin/sh
case "$1 $2" in
  'auth status')
    [ -z "$GH_TOKEN$GITHUB_TOKEN" ] || exit 1
    printf '%s' '{"hosts":{"github.com":[{"login":"bob"},{"login":"alice"}]}}' ;;
  'auth token')
    [ -z "$GH_TOKEN$GITHUB_TOKEN" ] && [ "$6" = alice ] || exit 2
    printf 'secret-alice\n' ;;
  'api graphql')
    case "$*" in *'--hostname github.com'*) ;; *) exit 5 ;; esac
    [ "$GH_TOKEN" = secret-alice ] && [ -z "$GITHUB_TOKEN" ] || exit 3
    printf '%s' '{"data":{"viewer":{"login":"alice"}}}' ;;
  *) exit 4 ;;
esac
"#,
        )
        .unwrap();
        std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o755)).unwrap();
        let gh = Gh::with_path(dir.path().as_os_str());
        assert_eq!(gh.accounts().await.unwrap(), ["alice", "bob"]);
        let token = gh.token("alice").await.unwrap();
        let data = gh
            .graphql(&token, "query { viewer { login } }", &serde_json::json!({}))
            .await
            .unwrap();
        assert_eq!(data["viewer"]["login"], "alice");
    }
    #[tokio::test]
    async fn graphql_errors_do_not_expose_the_token() {
        let dir = tempfile::tempdir().unwrap();
        let script = dir.path().join("gh");
        std::fs::write(
            &script,
            "#!/bin/sh\nprintf '%s' \"error: $GH_TOKEN\" >&2\nexit 1\n",
        )
        .unwrap();
        std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o755)).unwrap();
        let gh = Gh::with_path(dir.path().as_os_str());
        let error = gh
            .graphql(
                "private-token",
                "query { viewer { login } }",
                &serde_json::json!({}),
            )
            .await
            .unwrap_err();
        assert!(!error.contains("private-token"));
        assert!(error.contains("[redacted]"));
    }
}
