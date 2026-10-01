//! Allows at most one prompt, load, close, or delete to run for a session at a time.
//! An operation starts only when it acquires a guard and ends when that guard is
//! dropped. Once connection shutdown begins, no operation starts.

use std::{
    collections::{HashMap, hash_map::Entry},
    sync::{Arc, Mutex, MutexGuard},
};

use agent_client_protocol::schema::v1::SessionId;
use futures::channel::oneshot;

use crate::cancellation::PromptCancellation;

/// The session operation in progress. Only a prompt can be cancelled.
enum Operation {
    Prompt(PromptCancellation),
    Session,
}

#[derive(Clone, Default)]
pub struct SessionOperations(Arc<Mutex<OperationsState>>);

#[derive(Default)]
struct OperationsState {
    active: HashMap<SessionId, Operation>,
    closing: HashMap<SessionId, Option<oneshot::Sender<()>>>,
    drained: Option<oneshot::Sender<()>>,
    /// Set when connection shutdown begins.
    shutting_down: bool,
}

/// Keeps one session busy. Dropping it makes the session available again.
pub struct OperationGuard {
    operations: SessionOperations,
    session_id: SessionId,
}

/// Reserves a session while its active state is being released.
pub struct CloseGuard {
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
        self.acquire(session_id, Operation::Session)
    }

    pub fn try_delete(&self, session_id: &SessionId) -> Option<OperationGuard> {
        self.acquire(session_id, Operation::Session)
    }

    /// Cancels a prompt and waits for any operation to finish while refusing
    /// new operations for this session.
    pub async fn begin_close(&self, session_id: &SessionId) -> Option<CloseGuard> {
        let wait = {
            let mut state = self.lock();
            if state.shutting_down || state.closing.contains_key(session_id) {
                return None;
            }
            if let Some(operation) = state.active.get(session_id) {
                if let Operation::Prompt(cancellation) = operation {
                    cancellation.cancel();
                }
                let (sender, receiver) = oneshot::channel();
                state.closing.insert(session_id.clone(), Some(sender));
                Some(receiver)
            } else {
                state.closing.insert(session_id.clone(), None);
                None
            }
        };
        if let Some(wait) = wait {
            wait.await.expect("operation guard signals close");
        }
        Some(CloseGuard {
            operations: self.clone(),
            session_id: session_id.clone(),
        })
    }

    /// Signals the active prompt. Does nothing
    /// when the session is idle or is being loaded or deleted.
    pub fn cancel(&self, session_id: &SessionId) {
        let cancellation = match self.lock().active.get(session_id) {
            Some(Operation::Prompt(cancellation)) => Some(cancellation.clone()),
            Some(Operation::Session) | None => None,
        };
        if let Some(cancellation) = cancellation {
            cancellation.cancel();
        }
    }

    /// Rejects every later operation and cancels the active prompts.
    /// Repeating it is harmless.
    pub fn begin_shutdown(&self) {
        let mut state = self.lock();
        state.shutting_down = true;
        for operation in state.active.values() {
            if let Operation::Prompt(cancellation) = operation {
                cancellation.cancel();
            }
        }
    }

    pub fn is_shutting_down(&self) -> bool {
        self.lock().shutting_down
    }

    /// Begins shutdown, then waits until every active operation has dropped its
    /// guard. Called once, while the connection keeps running so active
    /// operations can send their replies.
    pub async fn shutdown(&self) {
        self.begin_shutdown();
        let (drained_tx, drained_rx) = oneshot::channel();
        {
            let mut state = self.lock();
            if state.active.is_empty() {
                return;
            }
            assert!(state.drained.is_none(), "shutdown is already waiting");
            state.drained = Some(drained_tx);
        }
        drained_rx
            .await
            .expect("the last operation signals shutdown");
    }

    fn acquire(&self, session_id: &SessionId, operation: Operation) -> Option<OperationGuard> {
        let mut state = self.lock();
        if state.shutting_down || state.closing.contains_key(session_id) {
            return None;
        }
        match state.active.entry(session_id.clone()) {
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
        if let Some(Some(waiter)) = state.closing.get_mut(&self.session_id).map(Option::take) {
            let _ = waiter.send(());
        }
        if state.active.is_empty()
            && let Some(drained) = state.drained.take()
        {
            let _ = drained.send(());
        }
    }
}

impl Drop for CloseGuard {
    fn drop(&mut self) {
        self.operations.lock().closing.remove(&self.session_id);
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
        assert!(operations.is_shutting_down());
        assert!(
            operations.try_prompt(&id("idle")).is_none(),
            "shutdown rejects operations for idle sessions"
        );
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

    #[test]
    fn close_cancels_a_prompt_and_reserves_the_session_until_cleanup_finishes() {
        let operations = SessionOperations::default();
        let session = id("a");
        let (guard, cancellation) = operations.try_prompt(&session).unwrap();
        let mut closing = Box::pin(operations.begin_close(&session));
        assert!(closing.as_mut().now_or_never().is_none());
        assert!(cancellation.is_cancelled());
        assert!(operations.try_load(&session).is_none());
        assert!(operations.try_prompt(&id("b")).is_some());
        drop(guard);
        let close_guard = closing.now_or_never().unwrap().unwrap();
        assert!(operations.try_prompt(&session).is_none());
        drop(close_guard);
        assert!(operations.try_load(&session).is_some());
    }
}
