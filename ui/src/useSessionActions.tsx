import { invoke } from "@tauri-apps/api/core";
import { useState } from "react";
import { Dialog } from "./Dialog";
import type { Archival, Deletion } from "./protocol";
import { showError } from "./showError";

/**
 * Deleting, archiving and unarchiving sessions, with the dialogs that ask
 * before losing unsaved work and report a kept branch. `busy` holds the
 * sessions whose actions are in flight; the caller renders `dialogs`.
 */
export function useSessionActions() {
  // The sessions whose archives, unarchives or deletes are in flight.
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set());
  // An archive or delete that would lose the unsaved work `message` lists.
  const [unsaved, setUnsaved] = useState<{
    sessionId: string;
    action: "archive" | "delete";
    message: string;
  } | null>(null);
  const [keptBranch, setKeptBranch] = useState<string | null>(null);

  // Runs `action` with the session's actions disabled.
  const withBusy = async (sessionId: string, action: () => Promise<void>) => {
    setBusy((busy) => new Set(busy).add(sessionId));
    try {
      await action();
    } catch (e) {
      await showError(e);
    } finally {
      setBusy((busy) => {
        const rest = new Set(busy);
        rest.delete(sessionId);
        return rest;
      });
    }
  };

  const deleteSession = (sessionId: string, discard: boolean) =>
    withBusy(sessionId, async () => {
      const deletion = await invoke<Deletion>("delete_session", { sessionId, discard });
      if (deletion.type === "unsaved_work") {
        setUnsaved({ sessionId, action: "delete", message: deletion.message });
      } else if (deletion.kept_branch !== null) {
        setKeptBranch(deletion.kept_branch);
      }
    });

  const archiveSession = (sessionId: string, discard: boolean) =>
    withBusy(sessionId, async () => {
      const archival = await invoke<Archival>("archive_session", { sessionId, discard });
      if (archival.type === "unsaved_work") {
        setUnsaved({ sessionId, action: "archive", message: archival.message });
      }
    });

  const unarchiveSession = (sessionId: string) =>
    withBusy(sessionId, () => invoke("unarchive_session", { sessionId }));

  const dialogs = (
    <>
      {unsaved !== null && (
        <Dialog
          title="Discard unsaved work?"
          onClose={() => setUnsaved(null)}
          actions={
            <>
              <button type="button" className="text-button" onClick={() => setUnsaved(null)}>
                Cancel
              </button>
              <button
                type="button"
                className="text-button"
                onClick={() => {
                  setUnsaved(null);
                  if (unsaved.action === "archive") archiveSession(unsaved.sessionId, true);
                  else deleteSession(unsaved.sessionId, true);
                }}
              >
                {unsaved.action === "archive" ? "Discard and Archive" : "Discard and Delete"}
              </button>
            </>
          }
        >
          <p className="message">{unsaved.message}</p>
        </Dialog>
      )}
      {keptBranch !== null && (
        <Dialog
          title="Branch kept"
          onClose={() => setKeptBranch(null)}
          actions={
            <button type="button" className="text-button" onClick={() => setKeptBranch(null)}>
              OK
            </button>
          }
        >
          <p className="message">
            Ox kept the branch <code>{keptBranch}</code>.
          </p>
        </Dialog>
      )}
    </>
  );

  return { busy, deleteSession, archiveSession, unarchiveSession, dialogs };
}

export type SessionActions = ReturnType<typeof useSessionActions>;
