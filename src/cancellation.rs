//! Shared cancellation signal for one active session operation.

use std::{
    future::Future,
    sync::{Arc, Mutex},
};

use futures::{
    FutureExt,
    channel::oneshot,
    future::{BoxFuture, Shared},
};

/// Once cancelled, every clone continues to observe cancellation; cancelling
/// twice is harmless.
#[derive(Clone)]
pub struct PromptCancellation(Arc<CancellationState>);

struct CancellationState {
    signal_tx: Mutex<Option<oneshot::Sender<()>>>,
    signal_rx: Shared<BoxFuture<'static, ()>>,
}

impl PromptCancellation {
    pub(crate) fn new() -> Self {
        let (signal_tx, signal_rx) = oneshot::channel();
        Self(Arc::new(CancellationState {
            signal_tx: Mutex::new(Some(signal_tx)),
            signal_rx: signal_rx.map(|_| ()).boxed().shared(),
        }))
    }

    pub fn cancelled(&self) -> impl Future<Output = ()> + Send + 'static + use<> {
        self.0.signal_rx.clone()
    }

    pub fn is_cancelled(&self) -> bool {
        self.0.signal_rx.clone().now_or_never().is_some()
    }

    pub fn cancel(&self) {
        let signal_tx = self
            .0
            .signal_tx
            .lock()
            .expect("prompt cancellation mutex poisoned")
            .take();
        if let Some(signal_tx) = signal_tx {
            let _ = signal_tx.send(());
        }
    }
}
