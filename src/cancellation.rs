//! Shared cancellation signal for one active session operation.

use std::{future::Future, sync::Arc};

use tokio::sync::watch;

/// Once cancelled, every clone continues to observe cancellation; cancelling
/// twice is harmless.
#[derive(Clone)]
pub struct PromptCancellation(Arc<watch::Sender<bool>>);

impl PromptCancellation {
    pub(crate) fn new() -> Self {
        Self(Arc::new(watch::Sender::new(false)))
    }

    pub fn cancelled(&self) -> impl Future<Output = ()> + Send + 'static + use<> {
        let mut receiver = self.0.subscribe();
        async move {
            let _ = receiver.wait_for(|cancelled| *cancelled).await;
        }
    }

    pub fn is_cancelled(&self) -> bool {
        *self.0.borrow()
    }

    pub fn cancel(&self) {
        self.0.send_replace(true);
    }
}
