import { invoke } from "@tauri-apps/api/core";
import { useEffect, useId, useState } from "react";
import { Dialog } from "./Dialog";
import type { Branches, SessionInfo } from "./protocol";
import { showError } from "./showError";

/**
 * Asks for the branch of a new worktree session and the commit a new branch
 * starts from, suggesting the workspace's branches, and starts the session.
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
  const [branches, setBranches] = useState<Branches>({ local: [], remote: [], fetch_error: null });
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [branch, setBranch] = useState("");
  const [start, setStart] = useState("");
  const [sending, setSending] = useState(false);
  const form = useId();
  const suggestions = useId();

  // The branches as they are, then again once the remotes are fetched.
  useEffect(() => {
    let open = true;
    (async () => {
      try {
        const listed = await invoke<Branches>("workspace_branches", { workspaceId, fetch: false });
        if (!open) return;
        setBranches(listed);
        const fetched = await invoke<Branches>("workspace_branches", { workspaceId, fetch: true });
        if (!open) return;
        if (fetched.fetch_error === null) setBranches(fetched);
        else setFetchError(fetched.fetch_error);
      } catch (e) {
        if (open) await showError(e);
      }
    })();
    return () => {
      open = false;
    };
  }, [workspaceId]);

  // Only a new branch has a starting point.
  const existing =
    branches.local.includes(branch) ||
    branches.remote.some(
      (remote) => remote === branch || remote.slice(remote.indexOf("/") + 1) === branch,
    );

  const create = async () => {
    setSending(true);
    try {
      const session = await invoke<SessionInfo>("new_session", {
        agent,
        workspaceId,
        checkout: { type: "worktree", branch, start: existing ? null : start || null },
      });
      onCreated(session);
    } catch (e) {
      setSending(false);
      await showError(e);
    }
  };

  return (
    <Dialog
      title="New Worktree on Branch"
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
          <input
            className="text-field"
            name="branch"
            required
            list={suggestions}
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
            disabled={existing}
            value={existing ? "" : start}
            onChange={(event) => setStart(event.target.value)}
          />
        </label>
        <datalist id={suggestions}>
          {[...branches.local, ...branches.remote].map((name) => (
            <option key={name} value={name} />
          ))}
        </datalist>
      </form>
      {fetchError !== null && (
        <p className="message fetch-error">Could not fetch the remotes: {fetchError}</p>
      )}
    </Dialog>
  );
}
