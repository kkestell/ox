import { invoke } from "@tauri-apps/api/core";
import { useEffect, useState } from "react";
import type { Branches } from "./protocol";
import { showError } from "./showError";

/**
 * The workspace's branches as they stand, then again once the remotes are
 * fetched. A failed fetch keeps the branches as they stood and sets
 * `fetchError`.
 */
export function useBranches(workspaceId: string) {
  const [branches, setBranches] = useState<Branches>({ local: [], remote: [], fetch_error: null });
  const [fetchError, setFetchError] = useState<string | null>(null);

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

  return { branches, fetchError };
}
