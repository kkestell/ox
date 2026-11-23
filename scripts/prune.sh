#!/bin/sh
# prune.sh removes each local branch whose pull request has merged, and the
# worktree that has it checked out. A branch counts as merged when its head is
# in the merged pull request's head, which GitHub's update-branch can move
# past it. A branch with commits beyond the merged head, or a worktree with
# uncommitted changes, stays.
set -eu

merged=$(gh pr list --state merged --limit 1000 --json headRefName,number -q '.[] | "\(.headRefName) \(.number)"')

for branch in $(git for-each-ref --format='%(refname:short)' refs/heads/); do
	numbers=$(echo "$merged" | awk -v b="$branch" '$1 == b {print $2}')
	found=
	for number in $numbers; do
		git fetch -q origin "pull/$number/head" || continue
		if git merge-base --is-ancestor "$branch" FETCH_HEAD; then
			found=1
			break
		fi
	done
	[ -n "$found" ] || continue
	worktree=$(git worktree list --porcelain | awk -v ref="branch refs/heads/$branch" '/^worktree /{w=substr($0, 10)} $0 == ref {print w}')
	if [ -n "$worktree" ]; then
		git worktree remove "$worktree" || continue
	fi
	git branch -D "$branch"
done
