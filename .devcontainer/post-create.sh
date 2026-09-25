#!/usr/bin/env bash
# Runs once per created container: seeds the user-level config for Claude Code
# and OpenCode, then reports tool and auth status.
set -euo pipefail

DC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The config volumes are seeded from the image, but a volume created before the
# image knew about them can still be root-owned; fix that once so seeding works.
ensure_writable() {
  local dir="$1"
  if [ ! -d "$dir" ]; then
    mkdir -p "$dir" 2>/dev/null || sudo mkdir -p "$dir"
  fi
  if [ ! -w "$dir" ]; then
    sudo chown -R "$(id -u):$(id -g)" "$dir"
  fi
}
ensure_writable "$HOME/.claude"
ensure_writable "$HOME/.config/opencode"

# Copy only when missing, so a container recreated on top of the config volumes
# keeps the settings and credentials already stored there.
seed() {
  local src="$1" dest="$2"
  mkdir -p "$(dirname "$dest")"
  if [ -e "$dest" ]; then
    echo "kept    $dest"
  else
    cp "$src" "$dest"
    echo "created $dest"
  fi
}

seed "$DC_DIR/config/claude-settings.json" "$HOME/.claude/settings.json"
seed "$DC_DIR/config/opencode.json" "$HOME/.config/opencode/opencode.json"

echo
echo "Tool versions:"
echo "  bun       $(bun --version)"
echo "  opencode  $(opencode --version)"
echo "  claude    $(claude --version 2>&1 | tail -n 1)"
echo "  go        $(go version 2>/dev/null | awk '{print $3}' || echo 'not installed')"
echo "  python    $(python3 --version 2>&1 | awk '{print $2}')"

echo
if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
  echo "ANTHROPIC_API_KEY is set: claude and opencode can both run."
else
  echo "No ANTHROPIC_API_KEY in this container. Authenticate once with one of:"
  echo "  claude          # then /login"
  echo "  opencode auth login"
  echo "Credentials are kept in the gogoform-*-config volumes, so they survive"
  echo "container rebuilds. To pass the key from the host instead, export"
  echo "ANTHROPIC_API_KEY before rebuilding this dev container."
fi

if command -v docker >/dev/null 2>&1; then
  echo
  echo "Docker is available in the container: 'make db-up' starts PostgreSQL."
fi
