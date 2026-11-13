// The wording of a local merge session's git status, shared by its row and
// its Merge button.

import type { Comparison, SessionInfo } from "./protocol";

function commits(count: number) {
  return count === 1 ? "1 commit" : `${count} commits`;
}

/** A local merge row's status icon, or null for no icon. */
export function branchStatus(
  compare: string,
  comparison: Comparison,
): { attention: boolean; tooltip: string } | null {
  if (comparison.type === "failed") return { attention: true, tooltip: comparison.error };
  const { ahead, behind } = comparison;
  if (ahead === 0) return null;
  if (behind === 0) {
    return { attention: ahead > 1, tooltip: `${commits(ahead)} ahead of ${compare}` };
  }
  return { attention: true, tooltip: `${commits(ahead)} ahead, ${behind} behind ${compare}` };
}

/** Why Merge into `compare` is disabled, or null when it is enabled. */
export function mergeBlocker(compare: string, comparison: Comparison): string | null {
  if (comparison.type === "failed") return comparison.error;
  if (comparison.ahead === 0) return "No commits";
  if (comparison.behind > 0) return `Behind ${compare}: ask the agent to rebase`;
  if (comparison.ahead > 1) return `${comparison.ahead} commits: ask the agent to squash`;
  return null;
}

/**
 * The comparison ref and comparison of a session that follows local merge:
 * one not archived, with a comparison ref. Null for other sessions.
 */
export function localMerge(
  session: SessionInfo,
): { compare: string; comparison: Comparison } | null {
  const compare = session.worktree?.compare;
  if (session.archived || compare == null || session.comparison === null) return null;
  return { compare, comparison: session.comparison };
}
