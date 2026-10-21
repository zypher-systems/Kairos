#!/bin/sh
# Kairos agent installer. Served by the kairos server at
#   GET /api/v1/agent/install.sh
# Usage:
#   curl -fsSL http://kairos:8080/api/v1/agent/install.sh | sh -s -- \
#     --server http://kairos:8080 --token <token> [--name worker-1] [--labels role=worker]
set -eu

SERVER_URL=""
TOKEN=""
AGENT_NAME=""
LABELS=""
PREFIX="/usr/local"
UNIT_PATH="/etc/systemd/system/kairos-agent.service"

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER_URL="$2"; shift 2 ;;
    --token)  TOKEN="$2"; shift 2 ;;
    --name)   AGENT_NAME="$2"; shift 2 ;;
    --labels) LABELS="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

[ -n "$SERVER_URL" ] || { echo "--server is required" >&2; exit 2; }
[ -n "$TOKEN" ] || { echo "--token is required" >&2; exit 2; }
SERVER_URL="${SERVER_URL%/}"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

as_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    echo "need root or sudo to install" >&2
    exit 1
  fi
}

echo ">> downloading kairos-agent (linux/$ARCH) from $SERVER_URL"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$SERVER_URL/api/v1/agent/download/linux/$ARCH" -o "$TMP"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$TMP" "$SERVER_URL/api/v1/agent/download/linux/$ARCH"
else
  echo "need curl or wget" >&2
  exit 1
fi

as_root install -m 0755 "$TMP" "$PREFIX/bin/kairos-agent"
echo ">> installed $PREFIX/bin/kairos-agent"

if [ -z "$AGENT_NAME" ]; then
  AGENT_NAME="$(hostname 2>/dev/null || echo worker)"
fi

if command -v systemctl >/dev/null 2>&1; then
  echo ">> writing $UNIT_PATH"
  as_root tee "$UNIT_PATH" > /dev/null <<UNIT_EOF
[Unit]
Description=Kairos job agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=KAIROS_SERVER_URL=$SERVER_URL
Environment=KAIROS_AGENT_TOKEN=$TOKEN
Environment=KAIROS_AGENT_NAME=$AGENT_NAME
Environment=KAIROS_AGENT_LABELS=$LABELS
ExecStart=$PREFIX/bin/kairos-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT_EOF
  as_root systemctl daemon-reload
  as_root systemctl enable --now kairos-agent
  echo ">> kairos-agent service enabled and started"
else
  echo ">> systemd not found - start manually:"
  echo "   KAIROS_SERVER_URL=$SERVER_URL KAIROS_AGENT_TOKEN=<token> $PREFIX/bin/kairos-agent"
fi

echo ">> done. The agent should appear in the kairos UI within seconds."
