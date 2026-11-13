import { invoke } from "@tauri-apps/api/core";
import { useId, useState } from "react";
import { Dialog } from "./Dialog";
import type { SessionInfo } from "./protocol";
import { showError } from "./showError";
import { useBranches } from "./useBranches";

/**
 * Asks for the name of a new branch for a new worktree session and the
 * commit it starts from, suggesting the workspace's branches as starting
 * points, and starts the session. A name a local or remote branch has is
 * refused before Create.
 */
export function NewBranchDialog({
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
  const [start, setStart] = useState("");
  const [sending, setSending] = useState(false);
  const form = useId();
  const suggestions = useId();

  const exists =
    branches.local.some((local) => local.name === branch) ||
    branches.remote.some(
      (remote) => remote === branch || remote.slice(remote.indexOf("/") + 1) === branch,
    );

  const create = async () => {
    setSending(true);
    try {
      const session = await invoke<SessionInfo>("new_session", {
        agent,
        workspaceId,
        checkout: { type: "worktree", branch: { type: "new", name: branch, start: start || null } },
      });
      onCreated(session);
    } catch (e) {
      setSending(false);
      await showError(e);
    }
  };

  return (
    <Dialog
      title="New Worktree on New Branch"
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
            disabled={sending || branch === "" || exists}
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
          <input
            className="text-field"
            name="branch"
            required
            value={branch}
            onChange={(event) => setBranch(event.target.value)}
          />
        </label>
        <label>
          Start From
          <input
            className="text-field"
            name="start"
            placeholder="HEAD"
            list={suggestions}
            value={start}
            onChange={(event) => setStart(event.target.value)}
          />
        </label>
        <datalist id={suggestions}>
          {[...branches.local.map((local) => local.name), ...branches.remote].map((name) => (
            <option key={name} value={name} />
          ))}
        </datalist>
      </form>
      {exists && <p className="message branch-exists">A branch named {branch} exists</p>}
      {fetchError !== null && (
        <p className="message fetch-error">Could not fetch the remotes: {fetchError}</p>
      )}
    </Dialog>
  );
}
