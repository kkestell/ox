//! Allows at most one prompt, load, or delete to run for a session at a time.
//! `/compact` arrives as a prompt request and runs as a prompt operation. An
//! operation starts only when it acquires a guard and ends when that guard is
//! dropped.

use std::{
    collections::{HashMap, hash_map::Entry},
    sync::{Arc, Mutex, MutexGuard},
};

use agent_client_protocol::schema::v1::SessionId;
use futures::channel::oneshot;

use crate::cancellation::PromptCancellation;

enum Operation {
    Prompt(PromptCancellation),
    Load,
    Delete,
}

#[derive(Clone, Default)]
pub struct SessionOperations(Arc<Mutex<OperationsState>>);

#[derive(Default)]
struct OperationsState {
    active: HashMap<SessionId, Operation>,
    drained: Option<oneshot::Sender<()>>,
}

/// Keeps one session busy. Dropping it makes the session available again.
pub struct OperationGuard {
    operations: SessionOperations,
    session_id: SessionId,
}

impl SessionOperations {
    pub fn try_prompt(
        &self,
        session_id: &SessionId,
    ) -> Option<(OperationGuard, PromptCancellation)> {
        let cancellation = PromptCancellation::new();
        self.acquire(session_id, Operation::Prompt(cancellation.clone()))
            .map(|guard| (guard, cancellation))
    }

    pub fn try_load(&self, session_id: &SessionId) -> Option<OperationGuard> {
        self.acquire(session_id, Operation::Load)
    }

    pub fn try_delete(&self, session_id: &SessionId) -> Option<OperationGuard> {
        self.acquire(session_id, Operation::Delete)
    }

    /// Signals the active prompt, including a running `/compact`. Does nothing
    /// when the session is idle or is being loaded or deleted.
    pub fn cancel(&self, session_id: &SessionId) {
        let cancellation = match self.lock().active.get(session_id) {
            Some(Operation::Prompt(cancellation)) => Some(cancellation.clone()),
            Some(Operation::Load | Operation::Delete) | None => None,
        };
        if let Some(cancellation) = cancellation {
            cancellation.cancel();
        }
    }

    /// Called once after incoming EOF, when no more operations can arrive.
    /// Keep the connection running until active operations send their replies.
    pub async fn shutdown(&self) {
        let (drained_tx, drained_rx) = oneshot::channel();
        {
            let mut state = self.lock();
            if state.active.is_empty() {
                return;
            }
            for operation in state.active.values() {
                if let Operation::Prompt(cancellation) = operation {
                    cancellation.cancel();
                }
            }
            assert!(state.drained.is_none(), "shutdown is already waiting");
            state.drained = Some(drained_tx);
        }
        drained_rx
            .await
            .expect("the last operation signals shutdown");
    }

    fn acquire(&self, session_id: &SessionId, operation: Operation) -> Option<OperationGuard> {
        match self.lock().active.entry(session_id.clone()) {
            Entry::Occupied(_) => None,
            Entry::Vacant(vacant) => {
                vacant.insert(operation);
                Some(OperationGuard {
                    operations: self.clone(),
                    session_id: session_id.clone(),
                })
            }
        }
    }

    fn lock(&self) -> MutexGuard<'_, OperationsState> {
        self.0.lock().expect("session operations mutex poisoned")
    }
}

impl Drop for OperationGuard {
    fn drop(&mut self) {
        let mut state = self.operations.lock();
        state.active.remove(&self.session_id);
        if state.active.is_empty()
            && let Some(drained) = state.drained.take()
        {
            let _ = drained.send(());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use futures::FutureExt;

    fn id(name: &str) -> SessionId {
        SessionId::new(name.to_owned())
    }

    #[test]
    fn a_guard_keeps_the_session_busy_until_it_drops() {
        let operations = SessionOperations::default();
        let session = id("a");
        let acquire_guards: [fn(&SessionOperations, &SessionId) -> Option<OperationGuard>; 3] = [
            |operations, session| operations.try_prompt(session).map(|(guard, _)| guard),
            |operations, session| operations.try_load(session),
            |operations, session| operations.try_delete(session),
        ];

        for acquire_guard in &acquire_guards {
            let guard = acquire_guard(&operations, &session).expect("an idle session is available");
            for incoming in &acquire_guards {
                assert!(
                    incoming(&operations, &session).is_none(),
                    "the session is busy"
                );
            }
            assert!(
                operations.try_prompt(&id("b")).is_some(),
                "other sessions are unaffected"
            );
            drop(guard);
        }
        assert!(
            operations.try_prompt(&session).is_some(),
            "dropping the guard made the session available"
        );
    }

    #[test]
    fn shutdown_cancels_all_prompts_and_waits_for_every_guard() {
        let operations = SessionOperations::default();
        let (guard_a, cancel_a) = operations.try_prompt(&id("a")).unwrap();
        let (guard_b, cancel_b) = operations.try_prompt(&id("b")).unwrap();
        let mut shutdown = Box::pin(operations.shutdown());

        assert!(shutdown.as_mut().now_or_never().is_none());
        assert!(cancel_a.is_cancelled());
        assert!(cancel_b.is_cancelled());
        assert!(operations.try_load(&id("a")).is_none());
        drop(guard_a);
        assert!(shutdown.as_mut().now_or_never().is_none());
        drop(guard_b);
        assert!(shutdown.now_or_never().is_some());
    }

    #[test]
    fn cancel_signals_only_the_active_prompt_for_that_session() {
        let operations = SessionOperations::default();
        let (guard_a, cancel_a) = operations.try_prompt(&id("a")).unwrap();
        let (_guard_b, cancel_b) = operations.try_prompt(&id("b")).unwrap();
        let _load_c = operations.try_load(&id("c")).unwrap();

        operations.cancel(&id("b"));
        assert!(cancel_b.is_cancelled());
        assert!(!cancel_a.is_cancelled());
        futures::executor::block_on(cancel_b.cancelled());
        operations.cancel(&id("b"));

        operations.cancel(&id("c"));
        operations.cancel(&id("idle"));

        drop(guard_a);
        operations.cancel(&id("a"));
        let (_guard, later) = operations.try_prompt(&id("a")).unwrap();
        assert!(
            !later.is_cancelled(),
            "a stale cancel does not reach a later prompt"
        );
    }
}
