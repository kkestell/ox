use keyring::{Entry, Error};
use std::{env, io};

const SERVICE: &str = "ox";
const ACCOUNT: &str = "OPENROUTER_API_KEY";

fn entry() -> io::Result<Entry> {
    Entry::new(SERVICE, ACCOUNT).map_err(keyring_error)
}

fn keyring_error(error: Error) -> io::Error {
    io::Error::other(format!("could not access the system keyring: {error}"))
}

pub fn api_key() -> io::Result<Option<String>> {
    api_key_with(env::var(ACCOUNT), || match entry()?.get_password() {
        Ok(api_key) if !api_key.is_empty() => Ok(Some(api_key)),
        Ok(_) | Err(Error::NoEntry) => Ok(None),
        Err(error) => Err(keyring_error(error)),
    })
}

fn api_key_with(
    environment: Result<String, env::VarError>,
    stored: impl FnOnce() -> io::Result<Option<String>>,
) -> io::Result<Option<String>> {
    match environment {
        Ok(api_key) if !api_key.is_empty() => return Ok(Some(api_key)),
        Ok(_) | Err(env::VarError::NotPresent) => {}
        Err(env::VarError::NotUnicode(_)) => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!("{ACCOUNT} is not valid UTF-8"),
            ));
        }
    }
    stored()
}

pub fn save_api_key(api_key: &str) -> io::Result<()> {
    entry()?.set_password(api_key).map_err(keyring_error)
}

pub fn delete_api_key() -> io::Result<bool> {
    match entry()?.delete_credential() {
        Ok(()) => Ok(true),
        Err(Error::NoEntry) => Ok(false),
        Err(error) => Err(keyring_error(error)),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn environment_key_takes_precedence_without_reading_the_keyring() {
        let api_key = api_key_with(Ok("from-environment".to_owned()), || {
            panic!("keyring should not be read when the environment key is set")
        })
        .unwrap();

        assert_eq!(api_key.as_deref(), Some("from-environment"));
    }

    #[test]
    fn missing_environment_key_uses_the_keyring() {
        let api_key = api_key_with(Err(env::VarError::NotPresent), || {
            Ok(Some("from-keyring".to_owned()))
        })
        .unwrap();

        assert_eq!(api_key.as_deref(), Some("from-keyring"));
    }
}
