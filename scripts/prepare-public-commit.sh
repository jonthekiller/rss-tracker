#!/bin/sh
# Refresh the `github-main` branch: a single-commit (orphan) branch holding the
# current state of `main`, used to publish a clean history to GitHub.
#
# The local repo keeps the full development history (pushed to the private
# remote); GitHub only ever receives the squashed commit. Paths in
# PUBLIC_EXCLUDE are stripped from the public commit (kept locally).
#
# Implemented with a temporary index (GIT_INDEX_FILE): the real index and the
# working tree are never touched.
#
# Usage: ./scripts/prepare-public-commit.sh ["commit message"]
#        ./scripts/prepare-public-commit.sh   (default message below)
set -eu
cd "$(dirname "$0")/.."

msg="${1:-RSS-Tracker v1.0.0 — initial public release}"

# Paths kept out of the public repository (still present locally).
# The MonitoRSS backup fixture contains real Discord guild/user/channel IDs.
PUBLIC_EXCLUDE='
scripts
internal/importer/testdata/monitorss-backup.json
'

tmp_index=$(mktemp)
trap 'rm -f "$tmp_index"' EXIT

GIT_INDEX_FILE=$tmp_index git read-tree main
for path in $PUBLIC_EXCLUDE; do
  GIT_INDEX_FILE=$tmp_index git rm -r --cached -q -- "$path"
done
tree=$(GIT_INDEX_FILE=$tmp_index git write-tree)
commit=$(git commit-tree "$tree" -m "$msg")
git branch -f github-main "$commit"

echo "github-main refreshed: single commit $commit (main minus excludes)."
echo "Verify with:"
echo "  git ls-tree --name-only github-main | grep -c scripts   -> 0"
echo "  git diff main github-main --stat                        -> excludes only"
