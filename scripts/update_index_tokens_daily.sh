#!/usr/bin/env bash
set -Eeuo pipefail

BASE_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

SOURCE_DIR="${INDEX_TOKEN_SOURCE_DIR:-/mnt/shared}"

INDEX_SOURCE="${SOURCE_DIR}/IndexTokens.csv"
BSE_SOURCE="${SOURCE_DIR}/BSEIndexTokens.csv"

INDEX_TARGET="${BASE_DIR}/IndexTokens.csv"
BSE_TARGET="${BASE_DIR}/BSEIndexTokens.csv"

STAMP_FILE="${BASE_DIR}/.index_tokens_synced_date"
TODAY="$(date +%F)"

require_nonempty_file() {
    local file="$1"
    if [[ ! -s "$file" ]]; then
        echo "[TOKENS] missing or empty file: $file" >&2
        exit 1
    fi
}

require_readable_csv() {
    local file="$1"
    local header
    require_nonempty_file "$file"
    header="$(head -n 1 "$file" | tr -d '\r')"
    if [[ "$header" != *,* ]]; then
        echo "[TOKENS] invalid CSV header in $file: $header" >&2
        exit 1
    fi
}

require_index_master() {
    local file="$1"
    require_readable_csv "$file"
    if ! head -n 1 "$file" | tr -d '\r' | grep -qE '(^|,)("Expiry"|Expiry)(,|$)'; then
        echo "[TOKENS] IndexTokens.csv is missing the Expiry column: $file" >&2
        exit 1
    fi
    if ! awk -F',' '
        NR > 1 &&
        ($4 ~ /"NIFTY"/ || $4 ~ /^"NIFTY/) &&
        $5 != "" {
            found = 1
        }
        END { exit found ? 0 : 1 }
    ' "$file"; then
        echo "[TOKENS] no NIFTY rows with an expiry value found in $file" >&2
        exit 1
    fi
}

# A previous "PERMANENT FIX" here reused the local copy for good once it
# existed and passed a loose validity check (has an Expiry column, has at
# least one NIFTY row) -- that check stays true forever even as the file
# rots, so it silently stopped re-syncing from /mnt/shared entirely: the
# local IndexTokens.csv sat unchanged from 2026-08-28 while /mnt/shared's
# copy moved on to 2026-09-18, missing three real, currently-listed NIFTY
# weekly expiries (06/13/19-OCT-26) that were never in the local copy at
# all -- confirmed live 2026-09-22 (the Terminal tab's expiry dropdown was
# missing exactly those three). Compare against the source's freshness
# every run instead of trusting a same-day stamp.
LOCAL_VALID=0
if [[ -s "$INDEX_TARGET" ]] && [[ -s "$BSE_TARGET" ]] &&
   require_index_master "$INDEX_TARGET" 2>/dev/null && require_readable_csv "$BSE_TARGET" 2>/dev/null; then
    LOCAL_VALID=1
fi

# Check if source is accessible (with timeout)
if ! timeout 5 test -f "$INDEX_SOURCE" 2>/dev/null; then
    if [[ "$LOCAL_VALID" == "1" ]]; then
        echo "[TOKENS] WARNING: ${SOURCE_DIR} not accessible, using existing local files"
        printf '%s\n' "$TODAY" > "$STAMP_FILE"
        echo "[TOKENS] using existing local files (source unavailable)"
        exit 0
    else
        echo "[TOKENS] ERROR: no valid local files and source unavailable" >&2
        exit 1
    fi
fi

if [[ "$LOCAL_VALID" == "1" ]]; then
    # Source is reachable: only skip the copy if it is not newer than what
    # we already have. mtime, not a once-a-day stamp, decides freshness.
    source_mtime="$(stat -c '%Y' "$INDEX_SOURCE" 2>/dev/null || echo 0)"
    target_mtime="$(stat -c '%Y' "$INDEX_TARGET" 2>/dev/null || echo 0)"
    if (( source_mtime <= target_mtime )); then
        echo "[TOKENS] local files already at or ahead of ${SOURCE_DIR} (source mtime ${source_mtime} <= local ${target_mtime}); skipping copy"
        printf '%s\n' "$TODAY" > "$STAMP_FILE"
        stat -c '[TOKENS] %y %s bytes %n' "$INDEX_TARGET" "$BSE_TARGET"
        exit 0
    fi
    echo "[TOKENS] ${SOURCE_DIR} is newer than the local copy; re-syncing"
fi

echo "[TOKENS] attempting sync from ${SOURCE_DIR}..."

# Source is accessible, proceed with normal sync
require_index_master "$INDEX_SOURCE"
require_readable_csv "$BSE_SOURCE"

tmp_index="$(mktemp "${BASE_DIR}/.IndexTokens.csv.XXXXXX")"
tmp_bse="$(mktemp "${BASE_DIR}/.BSEIndexTokens.csv.XXXXXX")"
trap 'rm -f "$tmp_index" "$tmp_bse"' EXIT

cp --preserve=mode,timestamps "$INDEX_SOURCE" "$tmp_index"
cp --preserve=mode,timestamps "$BSE_SOURCE" "$tmp_bse"

require_index_master "$tmp_index"
require_readable_csv "$tmp_bse"

install -m 0644 "$tmp_index" "$INDEX_TARGET"
install -m 0644 "$tmp_bse" "$BSE_TARGET"

printf '%s\n' "$TODAY" > "$STAMP_FILE"

echo "[TOKENS] synchronized from ${SOURCE_DIR} on ${TODAY}"
stat -c '[TOKENS] %y %s bytes %n' "$INDEX_TARGET" "$BSE_TARGET"
