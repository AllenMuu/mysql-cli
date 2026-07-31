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
