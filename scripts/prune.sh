#!/bin/sh
# prune.sh removes each local branch whose pull request has merged, and the
# worktree that has it checked out. A branch with commits beyond the merged
# head, or a worktree with uncommitted changes, stays.
set -eu

merged=$(gh pr list --state merged --limit 1000 --json headRefName,headRefOid -q '.[] | "\(.headRefName) \(.headRefOid)"')

for branch in $(git for-each-ref --format='%(refname:short)' refs/heads/); do
	echo "$merged" | grep -qx "$branch $(git rev-parse "$branch")" || continue
	worktree=$(git worktree list --porcelain | awk -v ref="branch refs/heads/$branch" '/^worktree /{w=substr($0, 10)} $0 == ref {print w}')
	if [ -n "$worktree" ]; then
		git worktree remove "$worktree" || continue
	fi
	git branch -D "$branch"
done
