#!/bin/sh
# Refresh the `github-main` branch: a single-commit (orphan) branch holding the
# current state of `main`, used to publish a clean history to GitHub.
#
# The local repo keeps the full development history (pushed to the private
# remote); GitHub only ever receives the squashed commit.
#
# Usage: ./scripts/prepare-public-commit.sh "commit message"
#        ./scripts/prepare-public-commit.sh   (default message below)
set -e
cd "$(dirname "$0")/.."

msg="${1:-RSS-Tracker v1.0.0 — initial public release}"

# Rebuild github-main from scratch so it always mirrors main's tree
git branch -D github-main 2>/dev/null || true
git checkout --orphan github-main
git commit -m "$msg"
git checkout main

echo "github-main refreshed: single commit, tree identical to main."
echo "Verify with: git diff main github-main   (should be empty)"
