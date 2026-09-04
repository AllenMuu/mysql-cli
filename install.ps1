# mysql-cli one-shot installer (Windows / PowerShell):
#   1) download the mysql-cli binary from the latest GitHub release
#   2) install agent skills via `npx skills add`
#   3) install per-agent write-confirmation configs via `mysql-cli agent init`
#
# Run in PowerShell (allow execution for this session):
#   Set-ExecutionPolicy -Scope Process Bypass -Force
#   .\install.ps1
$ErrorActionPreference = "Stop"

$Repo = "AllenMuu/mysql-cli"
$InstallDir = if ($env:MYSQL_CLI_INSTALL_DIR) { $env:MYSQL_CLI_INSTALL_DIR } else { Join-Path $env:USERPROFILE ".local\bin" }

function Info($m) { Write-Host "==> $m" -ForegroundColor Blue }
function Ok($m)   { Write-Host "OK  $m" -ForegroundColor Green }
function Warn($m) { Write-Host "!!  $m" -ForegroundColor Yellow }
function Err($m)  { Write-Host "XX  $m" -ForegroundColor Red }

# ---- 1. binary from the latest GitHub release ----
Info "Installing mysql-cli binary from latest release..."
# GoReleaser 不构建 windows/arm64 资产（.goreleaser.yml 显式 ignore 该组合），
# 下载 windows_arm64.zip 必 404。Windows 11 ARM64 可通过 x64 模拟运行 amd64
# 二进制，因此统一下载 amd64 资产。
$Archive = "mysql-cli_windows_amd64.zip"
$Url = "https://github.com/$Repo/releases/latest/download/$Archive"
# GoReleaser 生成单一 checksums.txt（每个资产行：<hash>  <asset_name>），不生成
# per-asset .sha256 文件。原代码尝试下载 $Url.sha256 永远 404，校验从未生效。
$ChecksumsUrl = "https://github.com/$Repo/releases/latest/download/checksums.txt"
$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("mysql-cli-" + [guid]::NewGuid().ToString())
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null
try {
	try {
		Invoke-WebRequest -Uri $Url -OutFile (Join-Path $Tmp $Archive) -UseBasicParsing
	} catch {
		Err "download failed: $Url"
		Err "alternative: go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest"
		exit 1
	}
	# 下载 checksums.txt；404（旧 release 没上传）则只警告继续，向后兼容。
	$ArchivePath = Join-Path $Tmp $Archive
	$ChecksumsPath = Join-Path $Tmp "checksums.txt"
	$hasChecksums = $true
	try {
		Invoke-WebRequest -Uri $ChecksumsUrl -OutFile $ChecksumsPath -UseBasicParsing
	} catch {
		$hasChecksums = $false
		Warn "WARNING: no checksums.txt available, cannot verify integrity"
	}
	if ($hasChecksums) {
		# 从 checksums.txt 中查找当前资产对应行的首列 hash。
		# checksums.txt 格式：<hash>  <asset_name>（两列以空白分隔）。
		$expected = $null
		foreach ($line in Get-Content $ChecksumsPath) {
			$parts = $line -split '\s+'
			if ($parts.Length -ge 2 -and $parts[1] -eq $Archive) {
				$expected = $parts[0]
				break
			}
		}
		if (-not $expected) {
			Warn "checksums.txt found but no entry for $Archive; cannot verify integrity"
		} else {
			$actual = (Get-FileHash -Path $ArchivePath -Algorithm SHA256).Hash.ToLower()
			if ($actual -ne $expected.ToLower()) {
				Err "checksum mismatch, possible download corruption"
				Err "expected: $expected"
				Err "actual:   $actual"
				exit 1
			} else {
				Ok "checksum verified"
			}
		}
	}
	Expand-Archive -Path (Join-Path $Tmp $Archive) -DestinationPath $Tmp -Force
	New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
	Move-Item -Force (Join-Path $Tmp "mysql-cli.exe") (Join-Path $InstallDir "mysql-cli.exe")
	Ok "binary -> $(Join-Path $InstallDir 'mysql-cli.exe')"
} finally {
	Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}
$Bin = Join-Path $InstallDir "mysql-cli.exe"

if ($env:PATH -notlike "*$InstallDir*") {
	Warn "$InstallDir not in PATH; add it or invoke via full path."
}

# ---- 2. agent skills ----
Info "Installing skills (npx skills add)..."
if (Get-Command npx -ErrorAction SilentlyContinue) {
	if (-not [Console]::IsInputRedirected) {
		npx --yes skills add AllenMuu/mysql-cli
	} else {
		Warn "non-interactive shell; run later: npx skills add AllenMuu/mysql-cli"
	}
} else {
	Warn "npx not found; install Node, then: npx skills add AllenMuu/mysql-cli"
}

# ---- 3. per-agent write-confirmation configs ----
Info "Installing write-confirmation configs (agent init)..."
& $Bin agent init --help 2>$null | Out-Null
if ($LASTEXITCODE -eq 0) {
	if (-not [Console]::IsInputRedirected) {
		& $Bin agent init
	} else {
		Warn "non-interactive shell; run later: $Bin agent init"
	}
} else {
	Warn "this release has no 'agent init' (needs v2.1+); re-run after upgrading."
	Warn "manual configs: https://github.com/$Repo/blob/main/docs/agent-integration.md"
}

Write-Host ""
Ok "Done. Next: $Bin config init --global   # then edit ~/.config/mysql-cli/config.toml"
Ok "Verify:    $Bin query 'SELECT 1'"
