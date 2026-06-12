#!/usr/bin/env bash
# Usage: bash scripts/setup-git-hooks.sh
# Installs project git hooks into .git/hooks so they run locally on every commit/push.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
HOOKS_SRC="$REPO_ROOT/.git-hooks"
HOOKS_DEST="$REPO_ROOT/.git/hooks"

if [ ! -d "$HOOKS_SRC" ]; then
  echo "Error: .git-hooks/ directory not found at $REPO_ROOT"
  exit 1
fi

echo "Installing git hooks..."

for hook in "$HOOKS_SRC"/*; do
  name=$(basename "$hook")
  dest="$HOOKS_DEST/$name"
  cp "$hook" "$dest"
  chmod +x "$dest"
  echo "  installed: .git/hooks/$name"
done

echo ""
echo "Done. Hooks installed:"
echo "  pre-commit  — blocks secrets, large binaries, debug artifacts"
echo "  commit-msg  — enforces Conventional Commits format"
echo "  pre-push    — runs go vet + go build before push to remote"
