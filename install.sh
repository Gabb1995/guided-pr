#!/bin/sh
# Installs the latest guided-pr release into ~/.local/bin (override with INSTALL_DIR=...).
#   curl -fsSL https://raw.githubusercontent.com/Gabb1995/guided-pr/main/install.sh | sh
set -eu

REPO="Gabb1995/guided-pr"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "Unsupported OS $(uname -s). On Windows, download the zip from https://github.com/$REPO/releases" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

asset="guided-pr_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/latest/download"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $asset…"
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

expected="$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null 2>&1; then actual="$(sha256sum "$tmp/$asset" | cut -d' ' -f1)"
else actual="$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)"; fi
[ -n "$expected" ] && [ "$expected" = "$actual" ] || { echo "Checksum mismatch for $asset" >&2; exit 1; }

tar -xzf "$tmp/$asset" -C "$tmp" guided-pr
mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/guided-pr" "$INSTALL_DIR/guided-pr"
echo "Installed $("$INSTALL_DIR/guided-pr" version) to $INSTALL_DIR/guided-pr"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "Add $INSTALL_DIR to your PATH, e.g.: echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.profile" ;;
esac
"$INSTALL_DIR/guided-pr" doctor || true
echo "Next: OpenCode: 'guided-pr install-skill --opencode'. Claude Code: '/plugin marketplace add Gabb1995/guided-pr' then '/plugin install guided-pr@guided-pr'."
