import { invoke } from "@tauri-apps/api/core";
import { useId, useState } from "react";
import { Dialog } from "./Dialog";
import type { SessionInfo } from "./protocol";
import { showError } from "./showError";
import { useBranches } from "./useBranches";

/**
 * Asks for an existing branch for a new worktree session, from the
 * workspace's local branches and the remote branches with no local branch of
 * the same name, and starts the session. A local branch that a worktree has
 * checked out is shown with that worktree and can't be chosen.
 */
export function ExistingBranchDialog({
  agent,
  workspaceId,
  onCreated,
  onClose,
}: {
  agent: string;
  workspaceId: string;
  onCreated: (session: SessionInfo) => void;
  onClose: () => void;
}) {
  const { branches, fetchError } = useBranches(workspaceId);
  const [branch, setBranch] = useState("");
  const [sending, setSending] = useState(false);
  const form = useId();

  const local = new Set(branches.local.map((local) => local.name));
  const remote = branches.remote.filter(
    (remote) => !local.has(remote) && !local.has(remote.slice(remote.indexOf("/") + 1)),
  );

  const create = async () => {
    setSending(true);
    try {
      const session = await invoke<SessionInfo>("new_session", {
        agent,
        workspaceId,
        checkout: { type: "worktree", branch: { type: "existing", name: branch } },
      });
      onCreated(session);
    } catch (e) {
      setSending(false);
      await showError(e);
    }
  };

  return (
    <Dialog
      title="New Worktree on Existing Branch"
      onClose={onClose}
      actions={
        <>
          <button type="button" className="text-button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className="text-button"
            form={form}
            disabled={sending || branch === ""}
          >
            Create
          </button>
        </>
      }
    >
      <form
        id={form}
        className="fields"
        onSubmit={(event) => {
          event.preventDefault();
          create();
        }}
      >
        <label>
          Branch
          <select
            className="text-field"
            name="branch"
            required
            value={branch}
            onChange={(event) => setBranch(event.target.value)}
          >
            <option value="" disabled />
            {branches.local.length > 0 && (
              <optgroup label="Local">
                {branches.local.map(({ name, worktree }) => (
                  <option key={name} value={name} disabled={worktree !== null}>
                    {worktree === null ? name : `${name} — checked out at ${worktree}`}
                  </option>
                ))}
              </optgroup>
            )}
            {remote.length > 0 && (
              <optgroup label="Remote">
                {remote.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </optgroup>
            )}
          </select>
        </label>
      </form>
      {fetchError !== null && (
        <p className="message fetch-error">Could not fetch the remotes: {fetchError}</p>
      )}
    </Dialog>
  );
}
