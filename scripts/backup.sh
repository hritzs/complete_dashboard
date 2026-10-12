#!/usr/bin/env bash
# Copies the trading platform into its own backup folder next to it:
#   ~/Desktop/api_gs/backup_trading-platform-git-recover_<date>_<time>_<label>
# (the Overnight NIFTY Long project keeps its own backups elsewhere).
#
# Usage: scripts/backup.sh [--complete] [label]
#   default     code, git history, .env, exports  (no logs)
#   --complete  also logs, ~/.trading-platform state (no feed recordings)
#               and a full database dump, under _state/
# Left out because start_platform.sh rebuilds them: node_modules, build.
set -euo pipefail
cd "$(dirname "$0")/.."
COMPLETE=0
if [ "${1:-}" = "--complete" ]; then COMPLETE=1; shift; fi
LABEL="$(printf '%s' "${1:-manual}" | tr -c 'A-Za-z0-9_.-' '_')"
[ "$COMPLETE" = 1 ] && LABEL="COMPLETE_$LABEL"
D="$(cd .. && pwd)/backup_$(basename "$PWD")_$(date +%Y%m%d_%H%M)_$LABEL"
[ -e "$D" ] && { echo "already exists: $D"; exit 1; }

EXCL=(--exclude node_modules --exclude build --exclude build_worker)
[ "$COMPLETE" = 1 ] || EXCL+=(--exclude logs)
rsync -a "${EXCL[@]}" ./ "$D/"
git -C "$D" fsck --no-dangling >/dev/null

if [ "$COMPLETE" = 1 ]; then
  mkdir -p "$D/_state"
  rsync -a --exclude feedrec "$HOME/.trading-platform/" "$D/_state/trading-platform-state/"
  DSN="$(set -a; . ./.env >/dev/null 2>&1; printf '%s' "${POSTGRES_DSN:-${DATABASE_URL:-}}")"
  if [ -n "$DSN" ]; then
    pg_dump "$DSN" 2>"$D/_state/pg_dump.err" | gzip > "$D/_state/database_$(date +%Y%m%d_%H%M).sql.gz" \
      || echo "database dump FAILED, see $D/_state/pg_dump.err"
  else
    echo "no POSTGRES_DSN in .env: database not dumped"
  fi
fi
{
  echo "taken:   $(date '+%Y-%m-%d %H:%M:%S %Z')"
  echo "kind:    $([ "$COMPLETE" = 1 ] && echo complete || echo code)"
  echo "commit:  $(git log --oneline -1)"
  echo "branch:  $(git rev-parse --abbrev-ref HEAD)"
  echo "not committed at backup time (included in this copy as they were):"
  git status --short | sed 's/^/  /'
} > "$D/BACKUP_INFO.txt"
echo "$D  ($(du -sh "$D" | cut -f1), commit $(git rev-parse --short HEAD))"
