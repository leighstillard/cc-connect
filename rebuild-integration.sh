#!/usr/bin/env bash
# rebuild-integration.sh — Rebuild local/integration from all open PRs
#
# Usage:
#   ./rebuild-integration.sh           # Rebuild from all open PRs
#   ./rebuild-integration.sh --dry-run # Show what would be merged
#
# This creates an ephemeral integration branch that merges all your open PRs.
# Run it whenever PRs update. Don't commit local-only changes to this branch
# — they'll be wiped on rebuild. Keep local changes on separate branches.

set -euo pipefail

INTEGRATION_BRANCH="local/integration"
DRY_RUN=false

if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi

log() { echo "[integration] $*"; }

# Get all open PR branches authored by current user
mapfile -t PR_BRANCHES < <(gh pr list --author @me --state open --json headRefName --jq '.[].headRefName')

if [[ ${#PR_BRANCHES[@]} -eq 0 ]]; then
  log "No open PRs found"
  exit 0
fi

log "Found ${#PR_BRANCHES[@]} open PRs:"
for branch in "${PR_BRANCHES[@]}"; do
  echo "  - $branch"
done

if $DRY_RUN; then
  log "Dry run — would merge these branches into $INTEGRATION_BRANCH"
  exit 0
fi

# Fetch latest
log "Fetching origin..."
git fetch origin --prune

# Stash any uncommitted changes
if ! git diff --quiet || ! git diff --cached --quiet; then
  log "Stashing uncommitted changes..."
  git stash push -m "rebuild-integration auto-stash"
  STASHED=true
else
  STASHED=false
fi

# Remember current branch to return to
ORIGINAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Create fresh integration branch from origin/main
log "Creating fresh $INTEGRATION_BRANCH from origin/main..."
git checkout -B "$INTEGRATION_BRANCH" origin/main

# Merge each PR branch
FAILED=()
for branch in "${PR_BRANCHES[@]}"; do
  log "Merging $branch..."
  if git merge --no-edit "origin/$branch" 2>/dev/null || git merge --no-edit "$branch" 2>/dev/null; then
    log "  OK"
  else
    log "  CONFLICT — aborting merge"
    git merge --abort
    FAILED+=("$branch")
  fi
done

# Report results
echo ""
log "Integration branch ready: $INTEGRATION_BRANCH"
if [[ ${#FAILED[@]} -gt 0 ]]; then
  log "Failed to merge (conflicts):"
  for branch in "${FAILED[@]}"; do
    echo "  - $branch"
  done
  log "Resolve manually: git merge <branch>"
fi

# Restore stash if we stashed
if $STASHED; then
  log "Restoring stashed changes..."
  git checkout "$ORIGINAL_BRANCH"
  git stash pop
fi
