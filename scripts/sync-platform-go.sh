#!/usr/bin/env sh
# Copy shared/platform-go from the meta-repo into third_party/ for standalone builds.
# Run from iag-project-management repo root:
#   sh scripts/sync-platform-go.sh
# Or from meta-repo:
#   sh services/commercial/project-management/scripts/sync-platform-go.sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
SRC="${IAG_PLATFORM_GO_SRC:-}"

if [ -z "$SRC" ]; then
  if [ -d "$ROOT/../../../shared/platform-go" ]; then
    SRC="$ROOT/../../../shared/platform-go"
  elif [ -d "$ROOT/../../shared/platform-go" ]; then
    SRC="$ROOT/../../shared/platform-go"
  else
    echo "Set IAG_PLATFORM_GO_SRC to the shared/platform-go directory" >&2
    exit 1
  fi
fi

DEST="$ROOT/third_party/platform-go"
mkdir -p "$DEST"
rm -rf "$DEST"/*
cp -R "$SRC"/. "$DEST/"
echo "Synced platform-go from $SRC to $DEST"

# chat-client (shared/services/chat-client) — vendored the same way so the
# standalone Docker build can resolve the cross-repo import.
CHAT_SRC="${IAG_CHAT_CLIENT_SRC:-}"
if [ -z "$CHAT_SRC" ]; then
  if [ -d "$ROOT/../../../shared/services/chat-client" ]; then
    CHAT_SRC="$ROOT/../../../shared/services/chat-client"
  elif [ -d "$ROOT/../../shared/services/chat-client" ]; then
    CHAT_SRC="$ROOT/../../shared/services/chat-client"
  fi
fi
if [ -n "$CHAT_SRC" ]; then
  CHAT_DEST="$ROOT/third_party/chat-client"
  mkdir -p "$CHAT_DEST"
  rm -rf "$CHAT_DEST"/*
  cp -R "$CHAT_SRC"/. "$CHAT_DEST/"
  echo "Synced chat-client from $CHAT_SRC to $CHAT_DEST"
else
  echo "warning: chat-client source not found — skipping (set IAG_CHAT_CLIENT_SRC)" >&2
fi
