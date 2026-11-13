import { invoke } from "@tauri-apps/api/core";
import { useState } from "react";
import { Dialog } from "./Dialog";
import { mergeBlocker } from "./localMerge";
import type { Comparison, SessionInfo } from "./protocol";
import { showError } from "./showError";

/**
 * Merge into `compare`: fast-forwards the ref's branch to the session's
 * branch, then offers to delete the session.
 */
export function MergeButton({
  session,
  compare,
  comparison,
  deleteSession,
}: {
  session: SessionInfo;
  compare: string;
  comparison: Comparison;
  deleteSession: (sessionId: string, discard: boolean) => Promise<void>;
}) {
  const [moving, setMoving] = useState(false);
  const [merged, setMerged] = useState(false);
  const blocker = mergeBlocker(compare, comparison);

  const merge = async () => {
    if (comparison.type !== "compared") return;
    setMoving(true);
    try {
      await invoke("move_branch", {
        workspaceId: session.workspace_id,
        branch: compare,
        to: comparison.tip,
        expected: comparison.compare_tip,
      });
      setMerged(true);
    } catch (e) {
      await showError(e);
    } finally {
      setMoving(false);
    }
  };

  return (
    <>
      <button
        type="button"
        className="text-button"
        title={blocker ?? undefined}
        disabled={blocker !== null || moving}
        onClick={merge}
      >
        Merge into {compare}
      </button>
      {merged && (
        <Dialog
          title={`Merged into ${compare}`}
          onClose={() => setMerged(false)}
          actions={
            <>
              <button type="button" className="text-button" onClick={() => setMerged(false)}>
                Keep Session
              </button>
              <button
                type="button"
                className="text-button"
                onClick={() => {
                  setMerged(false);
                  deleteSession(session.id, false);
                }}
              >
                Delete Session
              </button>
            </>
          }
        >
          <p className="message">
            Delete the session? Ox removes its worktree and its branch{" "}
            <code>{session.worktree?.branch}</code>.
          </p>
        </Dialog>
      )}
    </>
  );
}
