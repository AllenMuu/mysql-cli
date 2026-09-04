package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWarnUntrustedProject_Warns(t *testing.T) {
	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	entries := []config.PathEntry{
		{Path: "/global", Kind: "global", Trusted: true, Exists: true},
		{Path: "/proj/.config/mysql-cli/config.toml", Kind: "project", Trusted: false, Exists: true},
	}
	merged := &config.Config{Datasources: map[string]config.Datasource{}}
	err := g.warnUntrustedProject(entries, merged)
	require.NoError(t, err)
	assert.Contains(t, stderr.String(), "WARN")
	assert.Contains(t, stderr.String(), "untrusted project config")
	assert.NotContains(t, stderr.String(), "config trust") // no trust command (anti AI auto-trust)
}

func TestWarnUntrustedProject_StrictErrors(t *testing.T) {
	var stderr bytes.Buffer
	g := &Globals{eout: &stderr, StrictTrust: true}
	entries := []config.PathEntry{
		{Path: "/proj", Kind: "project", Trusted: false, Exists: true},
	}
	err := g.warnUntrustedProject(entries, &config.Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WARN")
	assert.Empty(t, stderr.String()) // strict returns err, does not also warn
}

func TestWarnUntrustedProject_NoTrustWarnFlag(t *testing.T) {
	var stderr bytes.Buffer
	g := &Globals{eout: &stderr, NoTrustWarn: true}
	entries := []config.PathEntry{{Path: "/p", Kind: "project", Trusted: false, Exists: true}}
	err := g.warnUntrustedProject(entries, &config.Config{})
	require.NoError(t, err)
	assert.Empty(t, stderr.String())
}

func TestWarnUntrustedProject_EnvSuppress(t *testing.T) {
	t.Setenv("MYSQL_CLI_NO_TRUST_WARN", "1")
	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	entries := []config.PathEntry{{Path: "/p", Kind: "project", Trusted: false, Exists: true}}
	err := g.warnUntrustedProject(entries, &config.Config{})
	require.NoError(t, err)
	assert.Empty(t, stderr.String())
}

func TestWarnUntrustedProject_AllTrusted(t *testing.T) {
	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	entries := []config.PathEntry{
		{Path: "/g", Kind: "global", Trusted: true, Exists: true},
		{Path: "/p", Kind: "project", Trusted: true, Exists: true},
	}
	err := g.warnUntrustedProject(entries, &config.Config{})
	require.NoError(t, err)
	assert.Empty(t, stderr.String())
}

func TestWarnUntrustedProject_NoFallback(t *testing.T) {
	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	entries := []config.PathEntry{{Path: "/p", Kind: "project", Trusted: false, Exists: true}}
	err := g.warnUntrustedProject(entries, nil)
	require.NoError(t, err)
	assert.Empty(t, stderr.String()) // no global fallback -> Load errors anyway, no warn
}

// writeExplicitConfig 在 dir 下创建一个显式 config 文件并返回其绝对路径。
func writeExplicitConfig(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(p, []byte("default = \"phish\"\n"), 0o600))
	return p
}

// TestWarnUntrustedExplicitConfig_RelativePath（B1）：显式 --config 传相对
// 路径、指向 cwd 内未信任项目的文件时，输出告警（非阻断）。
func TestWarnUntrustedExplicitConfig_RelativePath(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	writeExplicitConfig(t, proj)

	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	g.warnUntrustedExplicitConfig("config.toml", proj, home)

	assert.Contains(t, stderr.String(), "WARN")
	assert.Contains(t, stderr.String(), "explicit config")
	assert.Contains(t, stderr.String(), "config.toml")
	assert.Contains(t, stderr.String(), "Do not auto-trust")
}

// TestWarnUntrustedExplicitConfig_AbsoluteUnderCwd（B1）：绝对路径但位于
// cwd 之下同样告警。
func TestWarnUntrustedExplicitConfig_AbsoluteUnderCwd(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	cfgFile := writeExplicitConfig(t, proj)

	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	g.warnUntrustedExplicitConfig(cfgFile, proj, home)

	assert.Contains(t, stderr.String(), "WARN")
}

// TestWarnUntrustedExplicitConfig_AbsoluteOutsideCwd（B1）：指向 cwd 之外的
// 绝对路径（如用户 home 下的自有配置）不告警。
func TestWarnUntrustedExplicitConfig_AbsoluteOutsideCwd(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	outside := writeExplicitConfig(t, home)

	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	g.warnUntrustedExplicitConfig(outside, proj, home)

	assert.Empty(t, stderr.String())
}

// TestWarnUntrustedExplicitConfig_TrustedProject（B1）：项目已 trust 时不告警。
func TestWarnUntrustedExplicitConfig_TrustedProject(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	writeExplicitConfig(t, proj)
	require.NoError(t, config.AddTrust(home, proj))

	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	g.warnUntrustedExplicitConfig("config.toml", proj, home)

	assert.Empty(t, stderr.String())
}

// TestWarnUntrustedExplicitConfig_MissingFile（B1）：文件不存在时不会被
// 加载，无钓鱼面，不告警。
func TestWarnUntrustedExplicitConfig_MissingFile(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()

	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	g.warnUntrustedExplicitConfig("no-such-file.toml", proj, home)

	assert.Empty(t, stderr.String())
}

// TestWarnUntrustedExplicitConfig_Suppressed（B1）：--no-trust-warn 与
// MYSQL_CLI_NO_TRUST_WARN=1 均可抑制告警。
func TestWarnUntrustedExplicitConfig_Suppressed(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	writeExplicitConfig(t, proj)

	var stderr1 bytes.Buffer
	g1 := &Globals{eout: &stderr1, NoTrustWarn: true}
	g1.warnUntrustedExplicitConfig("config.toml", proj, home)
	assert.Empty(t, stderr1.String())

	t.Setenv("MYSQL_CLI_NO_TRUST_WARN", "1")
	var stderr2 bytes.Buffer
	g2 := &Globals{eout: &stderr2}
	g2.warnUntrustedExplicitConfig("config.toml", proj, home)
	assert.Empty(t, stderr2.String())
}

// TestWarnUntrustedExplicitConfig_SymlinkIntoCwd（F5a）：绝对路径本身在
// cwd 之外、但经 symlink 指向 cwd 内文件时同样告警。
func TestWarnUntrustedExplicitConfig_SymlinkIntoCwd(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	writeExplicitConfig(t, proj)
	writeExplicitConfig(t, home) // 对照组：symlink 指向 home 内文件

	linkIn := filepath.Join(home, "link-into-cwd.toml")
	require.NoError(t, os.Symlink(filepath.Join(proj, "config.toml"), linkIn))
	linkOut := filepath.Join(home, "link-outside.toml")
	require.NoError(t, os.Symlink(filepath.Join(home, "config.toml"), linkOut))

	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	g.warnUntrustedExplicitConfig(linkIn, proj, home)
	assert.Contains(t, stderr.String(), "WARN")

	var stderrOut bytes.Buffer
	gOut := &Globals{eout: &stderrOut}
	gOut.warnUntrustedExplicitConfig(linkOut, proj, home)
	assert.Empty(t, stderrOut.String(), "symlink pointing outside cwd/home stays silent")
}

// TestWarnUntrustedExplicitConfig_ProjectRootFromSubdir（F5b）：agent 在
// 项目子目录运行、--config 指向项目根的 config 时同样告警。
func TestWarnUntrustedExplicitConfig_ProjectRootFromSubdir(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	sub := filepath.Join(proj, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	// 项目根存在 .config/mysql-cli/config.toml，DiscoverProject 才能从子目录
	// 向上发现项目根。
	cfgDir := filepath.Join(proj, ".config", "mysql-cli")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	cfgFile := filepath.Join(cfgDir, "config.toml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("default = \"phish\"\n"), 0o600))

	var stderr bytes.Buffer
	g := &Globals{eout: &stderr}
	g.warnUntrustedExplicitConfig(cfgFile, sub, home)
	assert.Contains(t, stderr.String(), "WARN")
}

func TestConfigTrust_NonTTYRequiresYes(t *testing.T) {
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = orig })

	t.Setenv("HOME", t.TempDir())
	c := newConfigTrustCmd(&Globals{})
	c.SetOut(&bytes.Buffer{})
	c.SetArgs([]string{t.TempDir()})
	err := c.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-interactive")
}

func TestConfigTrust_NonTTYWithYes(t *testing.T) {
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = orig })

	home := t.TempDir()
	t.Setenv("HOME", home)
	target := t.TempDir()
	c := newConfigTrustCmd(&Globals{})
	c.SetOut(&bytes.Buffer{})
	c.SetArgs([]string{target, "--yes"})
	require.NoError(t, c.Execute())
	data, err := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	require.NoError(t, err)
	assert.Contains(t, string(data), target)
}
