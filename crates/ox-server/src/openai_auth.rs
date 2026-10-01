//! Ox's ChatGPT registration, browser authorization, and rotating credentials.

use std::{
    collections::BTreeMap,
    fs::{File, OpenOptions},
    io::{self, ErrorKind, Write},
    os::unix::fs::OpenOptionsExt,
    path::{Path, PathBuf},
    time::Duration,
};

use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use jsonwebtoken::{Algorithm, DecodingKey, Validation, decode, decode_header, jwk::JwkSet};
use reqwest::Url;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
};

const ISSUER: &str = "https://auth.openai.com";
const RESOURCE: &str = "https://api.openai.com/v1";
const PERMISSION: &str = "chatgpt.tokens.use.direct";
const SCOPES: &str =
    "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct";
const LOGIN_TIMEOUT: Duration = Duration::from_secs(600);

// Tokens deliberately have no Debug implementation.
#[derive(Clone, Serialize, Deserialize)]
pub struct Credentials {
    client_id: String,
    issuer: String,
    subject: String,
    email: Option<String>,
    scopes: Vec<String>,
    access_token: Option<String>,
    refresh_token: Option<String>,
    id_token: Option<String>,
    expires_at: i64,
    earliest_refresh_at: Option<i64>,
}

#[derive(Clone)]
pub struct Authentication {
    directory: PathBuf,
    issuer: String,
    http: reqwest::Client,
}

#[derive(Deserialize)]
struct Discovery {
    issuer: String,
    authorization_endpoint: String,
    token_endpoint: String,
    jwks_uri: String,
    revocation_endpoint: String,
}

#[derive(Deserialize)]
struct Tokens {
    access_token: String,
    refresh_token: String,
    id_token: Option<String>,
    token_type: String,
    expires_in: i64,
    scope: Option<String>,
    earliest_refresh_at: Option<i64>,
}

#[derive(Serialize, Deserialize)]
struct Identity {
    iss: String,
    sub: String,
    aud: serde_json::Value,
    exp: i64,
    iat: i64,
    nonce: String,
    email: Option<String>,
}

fn now() -> i64 {
    chrono::Utc::now().timestamp()
}

fn invalid(message: &str) -> io::Error {
    io::Error::new(ErrorKind::InvalidData, message)
}

fn login_required() -> io::Error {
    io::Error::new(
        ErrorKind::PermissionDenied,
        "OpenAI authentication required; run `ox auth login openai`",
    )
}

fn transport(error: reqwest::Error) -> io::Error {
    // Authorization URLs may carry a retained ID token.
    io::Error::other(format!(
        "OpenAI authentication request failed: {}",
        error.without_url()
    ))
}

fn random() -> String {
    URL_SAFE_NO_PAD.encode(
        [
            uuid::Uuid::new_v4().as_bytes().as_slice(),
            uuid::Uuid::new_v4().as_bytes().as_slice(),
        ]
        .concat(),
    )
}

fn protected_write(path: &Path, bytes: &[u8]) -> io::Result<()> {
    let directory = path.parent().expect("credential path has a parent");
    std::fs::create_dir_all(directory)?;
    let temporary = directory.join(format!(".openai-{}.tmp", uuid::Uuid::new_v4()));
    let result = (|| {
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&temporary)?;
        file.write_all(bytes)?;
        file.sync_all()?;
        std::fs::rename(&temporary, path)
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&temporary);
    }
    result
}

impl Authentication {
    #[cfg(test)]
    pub(crate) fn fixture(directory: PathBuf) -> Self {
        let auth = Self::at(directory, ISSUER.to_owned());
        auth.save(&Credentials {
            client_id: "oaiapp_fixture".to_owned(),
            issuer: ISSUER.to_owned(),
            subject: "fixture".to_owned(),
            email: None,
            scopes: vec![PERMISSION.to_owned()],
            access_token: Some("fixture-access-token".to_owned()),
            refresh_token: None,
            id_token: None,
            expires_at: now() + 3600,
            earliest_refresh_at: None,
        })
        .unwrap();
        auth
    }

    pub fn new() -> io::Result<Self> {
        Ok(Self::for_home(&crate::settings::home_dir()?))
    }

    pub(crate) fn for_home(home: &Path) -> Self {
        Self::at(home.join(".config/ox"), ISSUER.to_owned())
    }

    fn at(directory: PathBuf, issuer: String) -> Self {
        Self {
            directory,
            issuer,
            http: reqwest::Client::builder()
                .timeout(Duration::from_secs(30))
                .build()
                .expect("HTTP client builds"),
        }
    }

    fn path(&self) -> PathBuf {
        self.directory.join("openai.json")
    }

    fn read(&self) -> io::Result<Option<Credentials>> {
        match std::fs::read(self.path()) {
            Ok(bytes) => serde_json::from_slice(&bytes)
                .map(Some)
                .map_err(|_| invalid("Ox's saved OpenAI credentials are malformed")),
            Err(error) if error.kind() == ErrorKind::NotFound => Ok(None),
            Err(error) => Err(error),
        }
    }

    pub(crate) fn has_credentials(&self) -> io::Result<bool> {
        Ok(self.read()?.is_some_and(|credentials| {
            credentials.access_token.is_some() || credentials.refresh_token.is_some()
        }))
    }

    fn save(&self, credentials: &Credentials) -> io::Result<()> {
        protected_write(
            &self.path(),
            &serde_json::to_vec(credentials).expect("credentials serialize"),
        )
    }

    async fn lock(&self) -> io::Result<File> {
        let path = self.directory.join("openai.lock");
        tokio::task::spawn_blocking(move || {
            std::fs::create_dir_all(path.parent().expect("lock path has a parent"))?;
            let file = OpenOptions::new()
                .read(true)
                .write(true)
                .create(true)
                .truncate(false)
                .mode(0o600)
                .open(path)?;
            rustix::fs::flock(&file, rustix::fs::FlockOperation::LockExclusive)?;
            Ok(file)
        })
        .await
        .map_err(io::Error::other)?
    }

    async fn host_id(&self) -> io::Result<String> {
        let _lock = self.lock().await?;
        let path = self.directory.join("openai-host-id");
        match std::fs::read_to_string(&path) {
            Ok(id) => Ok(id),
            Err(error) if error.kind() == ErrorKind::NotFound => {
                let id = format!("urn:uuid:{}", uuid::Uuid::new_v4());
                protected_write(&path, id.as_bytes())?;
                Ok(id)
            }
            Err(error) => Err(error),
        }
    }

    async fn discovery(&self) -> io::Result<Discovery> {
        let response = self
            .http
            .get(format!("{}/.well-known/openid-configuration", self.issuer))
            .send()
            .await
            .map_err(transport)?;
        if !response.status().is_success() {
            return Err(io::Error::other(format!(
                "OpenAI discovery returned {}",
                response.status()
            )));
        }
        let discovery: Discovery = response.json().await.map_err(transport)?;
        if discovery.issuer != self.issuer {
            return Err(invalid("OpenAI discovery issuer did not match"));
        }
        Ok(discovery)
    }

    async fn exchange(&self, endpoint: &str, form: &[(&str, &str)]) -> io::Result<Tokens> {
        let response = self
            .http
            .post(endpoint)
            .form(form)
            .send()
            .await
            .map_err(transport)?;
        let status = response.status();
        if !status.is_success() {
            // Provider messages can echo secrets. Only the OAuth error code is diagnostic.
            let body: serde_json::Value = response.json().await.map_err(transport)?;
            let code = body["error"].as_str().unwrap_or("unknown_error");
            let code = match code {
                "invalid_grant"
                | "invalid_client"
                | "invalid_refresh_token"
                | "token_expired"
                | "refresh_token_expired"
                | "refresh_token_invalidated"
                | "refresh_token_reused"
                | "temporarily_unavailable"
                | "server_error" => code,
                _ => "unknown_error",
            };
            return Err(io::Error::new(
                ErrorKind::PermissionDenied,
                format!(
                    "OpenAI token exchange returned {status} ({code}); run `ox auth login openai`"
                ),
            ));
        }
        let tokens: Tokens = response.json().await.map_err(transport)?;
        if tokens.access_token.is_empty()
            || tokens.refresh_token.is_empty()
            || tokens.expires_in <= 0
            || !tokens.token_type.eq_ignore_ascii_case("bearer")
        {
            return Err(invalid("OpenAI returned incomplete credentials"));
        }
        Ok(tokens)
    }

    async fn identity(
        &self,
        discovery: &Discovery,
        token: &str,
        client_id: &str,
        nonce: &str,
    ) -> io::Result<Identity> {
        let header =
            decode_header(token).map_err(|_| invalid("OpenAI ID token header is invalid"))?;
        if !matches!(header.alg, Algorithm::RS256 | Algorithm::ES256) {
            return Err(invalid(
                "OpenAI ID token signature algorithm is unsupported",
            ));
        }
        let response = self
            .http
            .get(&discovery.jwks_uri)
            .send()
            .await
            .map_err(transport)?;
        if !response.status().is_success() {
            return Err(io::Error::other(format!(
                "OpenAI signing keys returned {}",
                response.status()
            )));
        }
        let keys: JwkSet = response.json().await.map_err(transport)?;
        let key = keys
            .find(
                header
                    .kid
                    .as_deref()
                    .ok_or_else(|| invalid("OpenAI ID token has no signing key ID"))?,
            )
            .ok_or_else(|| invalid("OpenAI ID token signing key is unknown"))?;
        let key =
            DecodingKey::from_jwk(key).map_err(|_| invalid("OpenAI signing key is invalid"))?;
        let mut validation = Validation::new(header.alg);
        validation.set_issuer(&[&discovery.issuer]);
        validation.set_audience(&[client_id]);
        validation.set_required_spec_claims(&["sub", "exp", "iat", "iss", "aud"]);
        validation.leeway = 5;
        let identity = decode::<Identity>(token, &key, &validation)
            .map_err(|_| invalid("OpenAI ID token signature or claims are invalid"))?
            .claims;
        if identity.sub.is_empty() || identity.nonce != nonce {
            return Err(invalid("OpenAI ID token subject or nonce is invalid"));
        }
        Ok(identity)
    }

    pub async fn login(&self) -> io::Result<()> {
        let replace = match self.read()? {
            Some(record)
                if record.access_token.is_none()
                    && record.refresh_token.is_none()
                    && record.id_token.is_none() =>
            {
                eprint!("Continue with the saved ChatGPT account? [Y/n]: ");
                io::stderr().flush()?;
                let mut answer = String::new();
                io::stdin().read_line(&mut answer)?;
                answer.trim().eq_ignore_ascii_case("n")
            }
            _ => false,
        };
        eprintln!("Continue with ChatGPT in your browser.");
        self.login_with_registration(open_browser, replace).await
    }

    async fn login_with_registration(
        &self,
        browser: impl Fn(&Url) -> io::Result<()>,
        replace: bool,
    ) -> io::Result<()> {
        let previous = self.read()?;
        if replace
            && previous.as_ref().is_some_and(|record| {
                record.access_token.is_some()
                    || record.refresh_token.is_some()
                    || record.id_token.is_some()
            })
        {
            return Err(invalid(
                "log out of OpenAI before replacing the saved account",
            ));
        }
        let saved = if replace { None } else { previous.clone() };
        let host_id = self.host_id().await?;
        let discovery = self.discovery().await?;
        let listener = TcpListener::bind("127.0.0.1:0").await?;
        let redirect_uri = format!(
            "http://127.0.0.1:{}/auth/callback",
            listener.local_addr()?.port()
        );
        let (state, nonce, verifier) = (random(), random(), random());
        let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()));
        let mut authorization = Url::parse(&discovery.authorization_endpoint)
            .map_err(|_| invalid("OpenAI authorization endpoint is invalid"))?;
        {
            let mut query = authorization.query_pairs_mut();
            query.extend_pairs([
                (
                    "client_id",
                    saved
                        .as_ref()
                        .map_or("dynamic_agent_client", |record| record.client_id.as_str()),
                ),
                ("ext_agent_host_id", &host_id),
                ("response_type", "code"),
                ("redirect_uri", &redirect_uri),
                ("scope", SCOPES),
                ("resource", RESOURCE),
                ("state", &state),
                ("nonce", &nonce),
                ("code_challenge_method", "S256"),
                ("code_challenge", &challenge),
            ]);
            match &saved {
                None => {
                    query.append_pair("agent_name_hint", "Ox");
                }
                Some(record) => {
                    if let Some(token) = &record.id_token {
                        query.append_pair("id_token_hint", token);
                    }
                    if let Some(email) = &record.email {
                        query.append_pair("login_hint", email);
                    }
                }
            }
        }
        browser(&authorization)?;
        let callback = tokio::time::timeout(LOGIN_TIMEOUT, callback(&listener))
            .await
            .map_err(|_| io::Error::new(ErrorKind::TimedOut, "OpenAI sign-in timed out"))??;
        if callback.get("state") != Some(&state) {
            return Err(invalid("OpenAI authorization state did not match"));
        }
        if callback.contains_key("error") {
            return Err(io::Error::new(
                ErrorKind::PermissionDenied,
                "OpenAI sign-in was declined or failed",
            ));
        }
        let client_id = match &saved {
            Some(record) => {
                if callback
                    .get("client_id")
                    .is_some_and(|id| id != &record.client_id)
                {
                    return Err(invalid("OpenAI callback changed the registered client ID"));
                }
                record.client_id.clone()
            }
            None => callback
                .get("client_id")
                .filter(|id| !id.is_empty() && id.as_str() != "dynamic_agent_client")
                .ok_or_else(|| invalid("OpenAI registration is incomplete: no issued client ID"))?
                .clone(),
        };
        let code = callback
            .get("code")
            .filter(|code| !code.is_empty())
            .ok_or_else(|| invalid("OpenAI callback has no authorization code"))?;
        let tokens = self
            .exchange(
                &discovery.token_endpoint,
                &[
                    ("grant_type", "authorization_code"),
                    ("client_id", &client_id),
                    ("code", code),
                    ("code_verifier", &verifier),
                    ("redirect_uri", &redirect_uri),
                    ("resource", RESOURCE),
                ],
            )
            .await?;
        let identity = self
            .identity(
                &discovery,
                tokens
                    .id_token
                    .as_deref()
                    .ok_or_else(|| invalid("OpenAI sign-in returned no ID token"))?,
                &client_id,
                &nonce,
            )
            .await?;
        if saved
            .as_ref()
            .is_some_and(|record| record.subject != identity.sub || record.issuer != identity.iss)
        {
            return Err(invalid(
                "OpenAI sign-in changed the saved account; log out before replacing it",
            ));
        }
        let scopes = tokens
            .scope
            .as_deref()
            .unwrap_or("")
            .split_whitespace()
            .map(str::to_owned)
            .collect::<Vec<_>>();
        if !scopes.iter().any(|scope| scope == PERMISSION) {
            return Err(io::Error::new(
                ErrorKind::PermissionDenied,
                "OpenAI sign-in did not grant ChatGPT plan permission",
            ));
        }
        let record = Credentials {
            client_id,
            issuer: identity.iss,
            subject: identity.sub,
            email: identity.email,
            scopes,
            access_token: Some(tokens.access_token),
            refresh_token: Some(tokens.refresh_token),
            id_token: tokens.id_token,
            expires_at: now()
                .checked_add(tokens.expires_in)
                .ok_or_else(|| invalid("OpenAI token expiry is invalid"))?,
            earliest_refresh_at: tokens.earliest_refresh_at,
        };
        // Do not hold the credential lock while the person signs in.
        let _lock = self.lock().await?;
        let current = self.read()?;
        if current
            .as_ref()
            .map(|record| (&record.client_id, &record.subject))
            != previous
                .as_ref()
                .map(|record| (&record.client_id, &record.subject))
        {
            return Err(invalid(
                "Ox's OpenAI registration changed during sign-in; try again",
            ));
        }
        self.save(&record)
    }

    pub async fn access_token(&self) -> io::Result<String> {
        let record = self.read()?.ok_or_else(login_required)?;
        if let Some(token) = valid_access_token(&record) {
            return Ok(token);
        }
        let _lock = self.lock().await?;
        let mut record = self.read()?.ok_or_else(login_required)?;
        if let Some(token) = valid_access_token(&record) {
            return Ok(token);
        }
        let refresh = record.refresh_token.as_deref().ok_or_else(login_required)?;
        if record
            .earliest_refresh_at
            .is_some_and(|earliest| earliest > now())
        {
            return Err(io::Error::new(
                ErrorKind::WouldBlock,
                "OpenAI token cannot be refreshed yet",
            ));
        }
        let discovery = self.discovery().await?;
        let tokens = self
            .exchange(
                &discovery.token_endpoint,
                &[
                    ("grant_type", "refresh_token"),
                    ("client_id", &record.client_id),
                    ("refresh_token", refresh),
                    ("resource", RESOURCE),
                ],
            )
            .await?;
        if let Some(scope) = &tokens.scope {
            record.scopes = scope.split_whitespace().map(str::to_owned).collect();
        }
        if !record.scopes.iter().any(|scope| scope == PERMISSION) {
            return Err(login_required());
        }
        record.access_token = Some(tokens.access_token);
        record.refresh_token = Some(tokens.refresh_token);
        if tokens.id_token.is_some() {
            record.id_token = tokens.id_token;
        }
        record.expires_at = now()
            .checked_add(tokens.expires_in)
            .ok_or_else(|| invalid("OpenAI token expiry is invalid"))?;
        record.earliest_refresh_at = tokens.earliest_refresh_at;
        let token = record
            .access_token
            .clone()
            .expect("replacement token was checked");
        self.save(&record)?;
        Ok(token)
    }

    /// Clears local tokens even when the service cannot confirm revocation.
    pub async fn logout(&self) -> io::Result<bool> {
        let _lock = self.lock().await?;
        let Some(mut record) = self.read()? else {
            return Ok(false);
        };
        let had_tokens = record.access_token.is_some()
            || record.refresh_token.is_some()
            || record.id_token.is_some();
        let revoked = if let Some(refresh) = &record.refresh_token {
            match self.discovery().await {
                Ok(discovery) => self
                    .http
                    .post(&discovery.revocation_endpoint)
                    .form(&[
                        ("token", refresh.as_str()),
                        ("token_type_hint", "refresh_token"),
                        ("client_id", record.client_id.as_str()),
                    ])
                    .send()
                    .await
                    .is_ok_and(|response| response.status().as_u16() == 200),
                Err(_) => false,
            }
        } else {
            true
        };
        record.access_token = None;
        record.refresh_token = None;
        record.id_token = None;
        record.expires_at = 0;
        record.earliest_refresh_at = None;
        self.save(&record)?;
        if !revoked {
            eprintln!(
                "OpenAI tokens removed locally; remote revocation was not confirmed. You can disconnect Ox in ChatGPT Settings."
            );
        }
        Ok(had_tokens)
    }
}

fn valid_access_token(record: &Credentials) -> Option<String> {
    (record.expires_at > now() + 60 && record.scopes.iter().any(|scope| scope == PERMISSION))
        .then(|| record.access_token.clone())
        .flatten()
}

fn open_browser(url: &Url) -> io::Result<()> {
    let program = if cfg!(target_os = "macos") {
        "open"
    } else {
        "xdg-open"
    };
    let status = std::process::Command::new(program)
        .arg(url.as_str())
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()?;
    if !status.success() {
        return Err(io::Error::other(
            "could not open the browser for OpenAI sign-in",
        ));
    }
    Ok(())
}

async fn callback(listener: &TcpListener) -> io::Result<BTreeMap<String, String>> {
    loop {
        let (mut socket, _) = listener.accept().await?;
        let mut bytes = Vec::new();
        let read = tokio::time::timeout(Duration::from_secs(5), async {
            let mut chunk = [0; 1024];
            while !bytes.windows(4).any(|part| part == b"\r\n\r\n") {
                let count = socket.read(&mut chunk).await?;
                if count == 0 || bytes.len() + count > 8192 {
                    return Err(invalid(
                        "OpenAI callback request is incomplete or too large",
                    ));
                }
                bytes.extend_from_slice(&chunk[..count]);
            }
            Ok(())
        })
        .await;
        if !matches!(read, Ok(Ok(()))) {
            continue;
        }
        let request = std::str::from_utf8(&bytes)
            .map_err(|_| invalid("OpenAI callback request is not UTF-8"))?;
        let mut line = request.lines().next().unwrap_or("").split_whitespace();
        if line.next() != Some("GET") {
            continue;
        }
        let url = Url::parse(&format!("http://127.0.0.1{}", line.next().unwrap_or("")))
            .map_err(|_| invalid("OpenAI callback URL is invalid"))?;
        if url.path() != "/auth/callback" {
            socket
                .write_all(b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n")
                .await?;
            continue;
        }
        let body = "Sign-in received. Return to Ox to see the result.";
        socket.write_all(format!("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).as_bytes()).await?;
        let mut pairs = BTreeMap::new();
        for (key, value) in url.query_pairs() {
            if pairs.insert(key.to_string(), value.to_string()).is_some() {
                return Err(invalid("OpenAI callback repeated a parameter"));
            }
        }
        return Ok(pairs);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::fixture::Workspace;
    use jsonwebtoken::{EncodingKey, Header, encode};
    use serde_json::{Value, json};
    use std::{
        os::unix::fs::PermissionsExt,
        sync::{Arc, Mutex},
    };

    struct Service {
        auth: Authentication,
        _workspace: Workspace,
        state: Arc<Mutex<Value>>,
        requests: Arc<Mutex<Vec<BTreeMap<String, String>>>>,
        task: tokio::task::JoinHandle<()>,
    }

    impl Drop for Service {
        fn drop(&mut self) {
            self.task.abort();
        }
    }

    impl Service {
        async fn new() -> Self {
            let workspace = Workspace::new();
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let issuer = format!("http://{}", listener.local_addr().unwrap());
            let auth = Authentication::at(workspace.0.clone(), issuer.clone());
            let state = Arc::new(Mutex::new(json!({"failure":"", "nonce":"", "rotations":0})));
            let requests = Arc::new(Mutex::new(Vec::new()));
            let (pending, seen) = (state.clone(), requests.clone());
            let task = tokio::spawn(async move {
                loop {
                    let (mut socket, _) = listener.accept().await.unwrap();
                    let mut bytes = Vec::new();
                    let (end, length) = loop {
                        let mut chunk = [0; 4096];
                        let count = socket.read(&mut chunk).await.unwrap();
                        assert_ne!(count, 0);
                        bytes.extend_from_slice(&chunk[..count]);
                        if let Some(end) = bytes.windows(4).position(|part| part == b"\r\n\r\n") {
                            let headers = String::from_utf8_lossy(&bytes[..end]);
                            let length = headers
                                .lines()
                                .find_map(|line| {
                                    line.to_ascii_lowercase()
                                        .strip_prefix("content-length:")
                                        .map(|length| length.trim().parse::<usize>().unwrap())
                                })
                                .unwrap_or(0);
                            if bytes.len() >= end + 4 + length {
                                break (end, length);
                            }
                        }
                    };
                    let headers = String::from_utf8_lossy(&bytes[..end]);
                    let path = headers
                        .lines()
                        .next()
                        .unwrap()
                        .split_whitespace()
                        .nth(1)
                        .unwrap();
                    let body = {
                        let mut state = pending.lock().unwrap();
                        let failure = state["failure"].as_str().unwrap().to_owned();
                        match path {
                            "/.well-known/openid-configuration" => json!({
                                "issuer": issuer, "authorization_endpoint": format!("{issuer}/authorize"),
                                "token_endpoint": format!("{issuer}/token"), "jwks_uri": format!("{issuer}/keys"),
                                "revocation_endpoint": format!("{issuer}/revoke"),
                            }),
                            "/keys" => json!({"keys":[{"kty":"RSA", "kid":"test", "use":"sig", "alg":"RS256", "n":MODULUS, "e":"AQAB"}]}),
                            "/revoke" => {
                                seen.lock().unwrap().push(form(&bytes[end+4..end+4+length]));
                                Value::Null
                            }
                            "/token" => {
                                let request = form(&bytes[end+4..end+4+length]);
                                assert_eq!(request["client_id"], "oaiapp_test");
                                assert_eq!(request["resource"], RESOURCE);
                                if request["grant_type"] == "authorization_code" {
                                    assert_eq!(request["redirect_uri"], state["redirect_uri"]);
                                    assert_eq!(URL_SAFE_NO_PAD.encode(Sha256::digest(request["code_verifier"].as_bytes())), state["challenge"]);
                                } else {
                                    assert_eq!(request["refresh_token"], format!("refresh-{}", state["rotations"].as_u64().unwrap()));
                                    assert!(!request.contains_key("scope"));
                                }
                                seen.lock().unwrap().push(request);
                                let rotations = state["rotations"].as_u64().unwrap() + 1;
                                state["rotations"] = json!(rotations);
                                let mut claims = json!({
                                    "iss":issuer, "sub":"account", "aud":"oaiapp_test", "exp":now()+3600, "iat":now(),
                                    "nonce":state["nonce"], "email":"example@example.test",
                                });
                                match failure.as_str() {
                                    "issuer" => claims["iss"] = json!("https://wrong.test"),
                                    "audience" => claims["aud"] = json!("another-client"),
                                    "nonce" => claims["nonce"] = json!("wrong"),
                                    "expiry" => claims["exp"] = json!(now()-100),
                                    "account" => claims["sub"] = json!("different-account"),
                                    _ => {}
                                }
                                let mut header = Header::new(Algorithm::RS256);
                                header.kid = Some("test".to_owned());
                                let mut token = encode(&header, &claims, &EncodingKey::from_rsa_der(&base64::engine::general_purpose::STANDARD.decode(SIGNING_KEY.lines().filter(|line| !line.starts_with("-----")).collect::<String>()).unwrap())).unwrap();
                                if failure == "signature" {
                                    let start = token.rfind('.').unwrap()+1;
                                    let changed = if &token[start..start+1] == "A" { "B" } else { "A" };
                                    token.replace_range(start..start+1, changed);
                                }
                                json!({
                                    "access_token":format!("access-{rotations}"), "refresh_token":format!("refresh-{rotations}"),
                                    "id_token":token, "expires_in":3600, "token_type":"Bearer",
                                    "scope": if failure == "permission" { "openid" } else { SCOPES },
                                })
                            }
                            _ => panic!("unexpected auth request {path}"),
                        }
                    }.to_string();
                    socket.write_all(format!("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).as_bytes()).await.unwrap();
                }
            });
            Self {
                auth,
                _workspace: workspace,
                state,
                requests,
                task,
            }
        }

        fn fail(&self, failure: &str) {
            self.state.lock().unwrap()["failure"] = json!(failure);
        }

        async fn login(&self) -> io::Result<()> {
            self.login_as(false).await
        }

        async fn login_as(&self, replace: bool) -> io::Result<()> {
            self.auth
                .login_with_registration(
                    |authorization| {
                        let query = authorization
                            .query_pairs()
                            .map(|(key, value)| (key.to_string(), value.to_string()))
                            .collect::<BTreeMap<_, _>>();
                        let mut state = self.state.lock().unwrap();
                        assert_eq!(query["scope"], SCOPES);
                        assert_eq!(query["resource"], RESOURCE);
                        assert_eq!(query["code_challenge_method"], "S256");
                        assert!(query["ext_agent_host_id"].starts_with("urn:uuid:"));
                        match self.auth.read().unwrap().filter(|_| !replace) {
                            Some(record) => {
                                assert_eq!(query["client_id"], record.client_id);
                                assert!(!query.contains_key("agent_name_hint"));
                                if let Some(token) = record.id_token {
                                    assert_eq!(query["id_token_hint"], token);
                                }
                            }
                            None => {
                                assert_eq!(query["client_id"], "dynamic_agent_client");
                                assert_eq!(query["agent_name_hint"], "Ox");
                            }
                        }
                        state["nonce"] = json!(query["nonce"]);
                        state["challenge"] = json!(query["code_challenge"]);
                        state["redirect_uri"] = json!(query["redirect_uri"]);
                        let mut callback = Url::parse(&query["redirect_uri"]).unwrap();
                        assert_eq!(callback.host_str(), Some("127.0.0.1"));
                        assert_eq!(callback.path(), "/auth/callback");
                        {
                            let mut pairs = callback.query_pairs_mut();
                            pairs.append_pair(
                                "state",
                                if state["failure"] == "state" {
                                    "wrong"
                                } else {
                                    &query["state"]
                                },
                            );
                            if state["failure"] == "denied" {
                                pairs.append_pair("error", "access_denied");
                            } else {
                                pairs.append_pair("code", "test-code");
                            }
                            if state["failure"] != "registration"
                                && query["client_id"] == "dynamic_agent_client"
                            {
                                pairs.append_pair("client_id", "oaiapp_test");
                            }
                            if state["failure"] == "client" {
                                pairs.append_pair("client_id", "oaiapp_wrong");
                            }
                        }
                        tokio::spawn(async move {
                            reqwest::get(callback).await.unwrap();
                        });
                        Ok(())
                    },
                    replace,
                )
                .await
        }
    }

    fn form(body: &[u8]) -> BTreeMap<String, String> {
        Url::parse(&format!("http://test/?{}", String::from_utf8_lossy(body)))
            .unwrap()
            .query_pairs()
            .map(|(key, value)| (key.to_string(), value.to_string()))
            .collect()
    }

    #[tokio::test]
    async fn registration_and_reauthorization_validate_identity_and_reuse_the_host() {
        let service = Service::new().await;
        service.login().await.unwrap();
        let host = service.auth.host_id().await.unwrap();
        let record = service.auth.read().unwrap().unwrap();
        assert_eq!(record.client_id, "oaiapp_test");
        assert_eq!(record.subject, "account");
        assert_eq!(service.auth.access_token().await.unwrap(), "access-1");
        assert_eq!(
            std::fs::metadata(service.auth.path())
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o600
        );
        std::fs::set_permissions(service.auth.path(), std::fs::Permissions::from_mode(0o644))
            .unwrap();
        service.login().await.unwrap();
        assert_eq!(
            std::fs::metadata(service.auth.path())
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o600
        );
        assert_eq!(service.auth.host_id().await.unwrap(), host);
        assert_eq!(service.auth.access_token().await.unwrap(), "access-2");
    }

    #[tokio::test]
    async fn account_replacement_requires_logout_and_preserves_the_host() {
        let service = Service::new().await;
        service.login().await.unwrap();
        let host = service.auth.host_id().await.unwrap();
        let saved = std::fs::read(service.auth.path()).unwrap();
        assert!(service.login_as(true).await.is_err());
        assert_eq!(std::fs::read(service.auth.path()).unwrap(), saved);
        service.auth.logout().await.unwrap();
        service.fail("account");
        service.login_as(true).await.unwrap();
        assert_eq!(
            service.auth.read().unwrap().unwrap().subject,
            "different-account"
        );
        assert_eq!(service.auth.host_id().await.unwrap(), host);
    }

    #[tokio::test]
    async fn rejected_sign_in_never_replaces_saved_credentials() {
        for failure in [
            "state",
            "denied",
            "client",
            "signature",
            "issuer",
            "audience",
            "nonce",
            "expiry",
            "account",
            "permission",
        ] {
            let service = Service::new().await;
            service.login().await.unwrap();
            let saved = std::fs::read(service.auth.path()).unwrap();
            service.fail(failure);
            assert!(service.login().await.is_err(), "{failure}");
            assert_eq!(
                std::fs::read(service.auth.path()).unwrap(),
                saved,
                "{failure}"
            );
        }
        let service = Service::new().await;
        service.fail("registration");
        assert!(
            service
                .login()
                .await
                .unwrap_err()
                .to_string()
                .contains("registration is incomplete")
        );
        assert!(service.auth.read().unwrap().is_none());
    }

    #[tokio::test]
    async fn refreshes_are_serialized_and_use_the_latest_rotating_token() {
        let service = Service::new().await;
        service.login().await.unwrap();
        let mut record = service.auth.read().unwrap().unwrap();
        record.expires_at = now() - 1;
        service.auth.save(&record).unwrap();
        let other = Authentication::at(service.auth.directory.clone(), service.auth.issuer.clone());
        let (a, b) = tokio::join!(service.auth.access_token(), other.access_token());
        assert_eq!(a.unwrap(), "access-2");
        assert_eq!(b.unwrap(), "access-2");
        record = service.auth.read().unwrap().unwrap();
        assert_eq!(record.refresh_token.as_deref(), Some("refresh-2"));
        record.expires_at = now() - 1;
        service.auth.save(&record).unwrap();
        assert_eq!(other.access_token().await.unwrap(), "access-3");
        let requests = service.requests.lock().unwrap();
        assert_eq!(requests.len(), 3);
        assert_eq!(requests[2]["refresh_token"], "refresh-2");
    }

    #[tokio::test]
    async fn logout_removes_tokens_and_retains_registration_and_host() {
        let service = Service::new().await;
        assert!(!service.auth.logout().await.unwrap());
        service.login().await.unwrap();
        let host = service.auth.host_id().await.unwrap();
        assert!(service.auth.logout().await.unwrap());
        let record = service.auth.read().unwrap().unwrap();
        assert_eq!(record.client_id, "oaiapp_test");
        assert_eq!(record.subject, "account");
        assert!(
            record.access_token.is_none()
                && record.refresh_token.is_none()
                && record.id_token.is_none()
        );
        assert_eq!(service.auth.host_id().await.unwrap(), host);
        assert!(
            service
                .auth
                .access_token()
                .await
                .unwrap_err()
                .to_string()
                .contains("ox auth login openai")
        );
        let requests = service.requests.lock().unwrap();
        assert_eq!(requests[1]["token"], "refresh-1");
        assert_eq!(requests[1]["token_type_hint"], "refresh_token");
    }

    #[tokio::test]
    async fn missing_credentials_name_the_explicit_login_command() {
        let service = Service::new().await;
        assert_eq!(
            service.auth.access_token().await.unwrap_err().to_string(),
            "OpenAI authentication required; run `ox auth login openai`"
        );
        assert!(service.requests.lock().unwrap().is_empty());
    }

    const SIGNING_KEY: &str = r#"-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA0lIjCX5mme28A9Ei2T5SXk7E3iFQNhGChpP2MRjmlBcRFlfl
x9c1XqfeaBOZlDcE2WffcIx/RweJDRGA/cKkgQTccS2BqOc2eMEaQLlQp+FA6U4u
5Ga3wXpABgOWNESnrOj7Qs0XL1if+AOzSsZkqFqnF7rOwXLOOM+XSziNK7QB7tTi
UrX0fiWcwix1VCkCLnM1+2F/rfDuB6jAlvIK7XsCBvb2UvXGwXcHIq0GI0IudBSv
7oHLGK7KdWDzMasGC3g5a4DeAT6/tx95X5krM0GAHZpzZgvzZnVy6Hp5FbjGYxgQ
VD+snZsNUkWzJH5Yrn4MO/KrsGvMv0aVBP3KEwIDAQABAoIBAAhoDw10l25EodS3
OcDcLFenp1fHlhirJ3/wjxEUUTcPGvg9KCqOMAxqAko/qk5RyqhT7grmGrpAk1pJ
3lGGQ6QCiTse2gVhxHwcH8wBfDdRmhIZNWecsXCSzddPsmPBcMBJCa34W5phXPeB
UlvFRUWzZeVdaqEx0QtVRNNVXdMmyrZPsmEhs+ovNYAuE2kulRgtJ23vJq2cAjtT
Yu5yXR5AhiH/yCRQ3QTkAFzV9uhETeH6w6fZT60PIcpzDxWTvEDQIiPWyFfp5ew2
te1Imex+WQbmcvtmw7PL4kLmagpjFXF9SIOedQLt3FBPFTRJDph2Wp3UctMwFYrT
d7DtcakCgYEA6OPwMHKQ0mjknhr2A8RVr/VtkKJk2tn4oPJdy6srICg8Nf6xlPLg
Sl/ytZIFHF+sy/E++bnpZ2RifLgNODn4rA9v2wiiZUIi57y/BeF9PJLl3kKnuwI4
1vtLgH8PZCDxGNoQEEIeYWJiwpdS9DsbgGAhREPyyygVOLXSrrIaY6sCgYEA5zDe
qncHqdbLES7/wW4DFVyFqPR3FjdweVJQD/WR5JouldbtKqNFlZ5y2Wx24OWfC5pG
ejBla+0Y3T76jLqJRfIGz0PyDGUsxEZZApFb4A46KIBCIuE8NTxX7C2j3jLf1Vo+
+OW3GVodWA28BDbPe11FXg/WxF+YIbQJeLs/yzkCgYEAjKAeh9KV+keWTJXxGYMz
ToW/PAejKLdXty/CTVo1Nzy0ZtI9PriNkLtxHgxnA6QN/jPVGAwXkPP/uFmWue3f
Z14G59bF4KjX1OCW5CEtcycsoFHVYEnOMpoZFCUlEQwHKT97VaXnHFzBT8j6MTmD
uLfTPppdACla7xxzdENdRfMCgYBh87Ozt8DYdbgN7MLRuoG18EB1KDa4g/60eGqR
iHMqzySDuc88bHbUAJEai7kGamNrcA8CQDUIeCk7vC49p973cqbt9BS+qbNA6alW
zC2IDZ8Yf9cFnjZ7O7joySGPyDBL3fOmUvWz2RlrsE4D3xEjbI4yXzWYeAQl63jD
3VoXCQKBgQCIZKmggdS2cCSvzOFMsUZpeg+fwAaOXWjVjQie2/P3kmDgBezpA28C
HpeMwKHi6yx1rwoGBAc+OcfLIi4QhIcuorQEiynTZSjzoNcgReYs01jmSqmzEThk
x6KLLlseWfIiLHHzkn+gKydFofyqmo7R9H0JDQg3pv0x90BgDmWyuw==
-----END RSA PRIVATE KEY-----
"#;
    const MODULUS: &str = "0lIjCX5mme28A9Ei2T5SXk7E3iFQNhGChpP2MRjmlBcRFlflx9c1XqfeaBOZlDcE2WffcIx_RweJDRGA_cKkgQTccS2BqOc2eMEaQLlQp-FA6U4u5Ga3wXpABgOWNESnrOj7Qs0XL1if-AOzSsZkqFqnF7rOwXLOOM-XSziNK7QB7tTiUrX0fiWcwix1VCkCLnM1-2F_rfDuB6jAlvIK7XsCBvb2UvXGwXcHIq0GI0IudBSv7oHLGK7KdWDzMasGC3g5a4DeAT6_tx95X5krM0GAHZpzZgvzZnVy6Hp5FbjGYxgQVD-snZsNUkWzJH5Yrn4MO_KrsGvMv0aVBP3KEw";
}
