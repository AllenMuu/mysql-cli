#!/usr/bin/env bash
# mysql-cli one-shot installer (macOS / Linux):
#   1) download the mysql-cli binary from the latest GitHub release
#   2) install agent skills via `npx skills add`
#   3) install per-agent write-confirmation configs via `mysql-cli agent init`
#
# Run it directly, not via `curl | bash`, so the interactive skills and
# agent-init prompts keep their TTY:
#   curl -fsSL https://raw.githubusercontent.com/AllenMuu/mysql-cli/main/install.sh -o install.sh
#   bash install.sh
set -euo pipefail

REPO="AllenMuu/mysql-cli"
INSTALL_DIR="${MYSQL_CLI_INSTALL_DIR:-$HOME/.local/bin}"

c_info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
c_ok()   { printf '\033[1;32mOK\033[0m  %s\n' "$1"; }
c_warn() { printf '\033[1;33m!!\033[0m  %s\n' "$1"; }
c_err()  { printf '\033[1;31mXX\033[0m  %s\n' "$1" >&2; }

# ---- 1. binary from the latest GitHub release ----
c_info "Installing mysql-cli binary from latest release..."
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
	x86_64|amd64) ARCH=amd64 ;;
	arm64|aarch64) ARCH=arm64 ;;
	*) c_err "unsupported architecture: $ARCH"; exit 1 ;;
esac
case "$OS" in
	darwin|linux) EXT=tar.gz ;;
	*) c_err "unsupported OS: $OS (on Windows use install.ps1)"; exit 1 ;;
esac
ARCHIVE="mysql-cli_${OS}_${ARCH}.${EXT}"
URL="https://github.com/${REPO}/releases/latest/download/${ARCHIVE}"
# GoReleaser 生成单一 checksums.txt（每个资产行：<hash>  <asset_name>），不生成
# per-asset .sha256 文件。原代码尝试下载 ${URL}.sha256 永远 404，校验从未生效。
CHECKSUMS_URL="https://github.com/${REPO}/releases/latest/download/checksums.txt"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
if ! curl -fsSL "$URL" -o "$TMP/$ARCHIVE"; then
	c_err "download failed: $URL"
	c_err "alternative: go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest"
	exit 1
fi
# 下载 checksums.txt；404（旧 release 没上传）则只警告继续，向后兼容。
if curl -fsSL "$CHECKSUMS_URL" -o "$TMP/checksums.txt"; then
	# 从 checksums.txt 中提取当前资产对应行的首列 hash。
	expected=$(awk -v want="$ARCHIVE" '$2 == want {print $1; exit}' "$TMP/checksums.txt")
	if [ -n "$expected" ]; then
		# 在 TMP 目录内执行校验，让相对路径匹配归档文件名。
		actual=$(cd "$TMP" && command -v sha256sum >/dev/null 2>&1 && sha256sum "$ARCHIVE" | awk '{print $1}')
		if [ -z "$actual" ] && command -v shasum >/dev/null 2>&1; then
			actual=$(cd "$TMP" && shasum -a 256 "$ARCHIVE" | awk '{print $1}')
		fi
		if [ -z "$actual" ]; then
			c_warn "no sha256sum/shasum tool found; skipping integrity check"
		elif [ "$actual" != "$expected" ]; then
			c_err "checksum mismatch, possible download corruption"
			c_err "expected: $expected"
			c_err "actual:   $actual"
			exit 1
		else
			c_ok "checksum verified"
		fi
	else
		c_warn "checksums.txt found but no entry for $ARCHIVE; cannot verify integrity"
	fi
else
	c_warn "WARNING: no checksums.txt available, cannot verify integrity"
fi
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
mkdir -p "$INSTALL_DIR"
mv -f "$TMP/mysql-cli" "$INSTALL_DIR/mysql-cli"
chmod +x "$INSTALL_DIR/mysql-cli"
c_ok "binary -> $INSTALL_DIR/mysql-cli"

case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*) c_warn "$INSTALL_DIR not in PATH. Add: export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
BIN="$INSTALL_DIR/mysql-cli"

# ---- 2. agent skills ----
c_info "Installing skills (npx skills add)..."
if command -v npx >/dev/null 2>&1; then
	if [ -t 0 ]; then
		npx --yes skills add AllenMuu/mysql-cli || c_warn "skills install incomplete (non-fatal)"
	else
		c_warn "non-interactive shell; run later: npx skills add AllenMuu/mysql-cli"
	fi
else
	c_warn "npx not found; install Node, then: npx skills add AllenMuu/mysql-cli"
fi

# ---- 3. per-agent write-confirmation configs ----
c_info "Installing write-confirmation configs (agent init)..."
if "$BIN" agent init --help >/dev/null 2>&1; then
	if [ -t 0 ]; then
		"$BIN" agent init || c_warn "agent init incomplete (non-fatal)"
	else
		c_warn "non-interactive shell; run later: $BIN agent init"
	fi
else
	c_warn "this release has no 'agent init' (needs v2.1+); re-run this script after upgrading."
	c_warn "manual configs: https://github.com/${REPO}/blob/main/docs/agent-integration.md"
fi

echo
c_ok "Done. Next: $BIN config init --global   # then edit ~/.config/mysql-cli/config.toml"
c_ok "Verify:    $BIN query 'SELECT 1'"
