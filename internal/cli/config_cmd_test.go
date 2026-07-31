package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigTrust_DefaultCwd(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	cfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("# stub"), 0o600)
	// chdir into a SUBDIR of projRoot (not projRoot itself) so DiscoverProject
	// must walk up to find projRoot/.config/mysql-cli/config.toml. If discovery
	// were broken (found=false), the fallback root would be this subdir, and
	// AddTrust would record the subdir -- not projRoot -- making the assertion
	// below genuinely distinguish the discovery path from the fallback.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)
	code := Run([]string{"config", "trust", "--yes"})
	assert.Equal(t, ExitOK, code)
	// Exact equality on the trimmed trust-file content: a BROKEN DiscoverProject
	// (found=false -> fallback root=dir=projRoot/sub) would record projRoot/sub,
	// and projRoot is a SUBSTRING of projRoot/sub (Contains would still pass).
	// EvalSymlinks normalizes macOS /var -> /private/var so the assertion is stable.
	b, err := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.NoError(t, err)
	want, _ := filepath.EvalSymlinks(projRoot)
	assert.Equal(t, want, strings.TrimSpace(string(b)))
}

func TestConfigTrust_Idempotent(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	cfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("# stub"), 0o600)
	// chdir into a subdir so DiscoverProject must walk up; if discovery were
	// broken the fallback would record the subdir, failing the assertion.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)
	assert.Equal(t, ExitOK, Run([]string{"config", "trust", "--yes"}))
	assert.Equal(t, ExitOK, Run([]string{"config", "trust", "--yes"})) // no duplicate
	// Exact equality proves idempotency: two trust calls must still produce a
	// single trimmed line == want. Substring Count would still pass for a
	// broken DiscoverProject (projRoot is a substring of projRoot/sub).
	b, err := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.NoError(t, err)
	want, _ := filepath.EvalSymlinks(projRoot)
	assert.Equal(t, want, strings.TrimSpace(string(b))) // single line == want => idempotent
}

func TestConfigTrust_JSON(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	cfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("# stub"), 0o600)
	// chdir into a subdir so DiscoverProject must walk up; if discovery were
	// broken the fallback would record the subdir, failing the assertion below.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)

	// Capture os.Stdout (config trust writes via cmd.OutOrStdout() -> os.Stdout).
	// Package tests are serial (no t.Parallel) so mutating global os.Stdout is
	// safe; restore via t.Cleanup registered BEFORE mutating os.Stdout so a
	// panic between the assignment and a later Cleanup registration cannot
	// leak the pipe-writer as os.Stdout.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	t.Cleanup(func() { os.Stdout = orig; r.Close() })
	os.Stdout = w
	code := Run([]string{"config", "trust", "-j", "--yes"})
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)

	assert.Equal(t, ExitOK, code)
	// MarshalIndent produces `"key": value` (colon+space); parse the envelope
	// to make the assertion robust against formatting drift.
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Trusted string `json:"trusted"`
		} `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(out, &env))
	assert.True(t, env.Success)
	assert.NotEmpty(t, env.Data.Trusted)
	// Also confirm the trusted path is projRoot (EvalSymlinks-normalized for macOS /var -> /private/var).
	want, _ := filepath.EvalSymlinks(projRoot)
	assert.Equal(t, want, env.Data.Trusted)
}

// TestConfigPath_ShowsProjectAndGlobal trusts projRoot first, then runs
// `config path` from a SUBDIR of projRoot so DiscoverProject must walk up to
// find projRoot/.config/mysql-cli/config.toml. It captures stdout and asserts
// content (not just exit code) so a broken discovery (no project line) or a
// broken format string (no "project:"/"global:" tags) fails the test.
func TestConfigPath_ShowsProjectAndGlobal(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	projCfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(projCfgDir, 0o755)
	os.WriteFile(filepath.Join(projCfgDir, "config.toml"), []byte("# p"), 0o600)
	// global config at home
	os.MkdirAll(filepath.Join(home, ".config", "mysql-cli"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "mysql-cli", "config.toml"), []byte("# g"), 0o600)
	// chdir into a SUBDIR of projRoot so DiscoverProject must walk up. If
	// discovery were broken (found=false), no "project:" line would appear and
	// the tag assertions below would fail.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)

	// Trust projRoot first so the project entry is [trusted], not [untrusted, skipped].
	assert.Equal(t, ExitOK, Run([]string{"config", "trust", "--yes"}))

	// Capture os.Stdout (config path writes via cmd.OutOrStdout() -> os.Stdout).
	// Pre-register t.Cleanup BEFORE mutating os.Stdout so a panic between the
	// assignment and a later Cleanup registration cannot leak the pipe-writer.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	t.Cleanup(func() { os.Stdout = orig; r.Close() })
	os.Stdout = w
	code := Run([]string{"config", "path"})
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)

	assert.Equal(t, ExitOK, code)
	// Tag substrings are fixed status tokens (not paths), so substring search
	// is safe here. They genuinely distinguish: (a) discovery worked (project:
	// present), (b) trust was recorded ([trusted] vs [untrusted, skipped]),
	// (c) global chain still listed (global:).
	assert.Contains(t, string(out), "project:")
	assert.Contains(t, string(out), "[trusted]")
	assert.Contains(t, string(out), "global:")
	// Belt-and-suspenders: the project path printed must reference projRoot
	// (EvalSymlinks-normalized for macOS /var -> /private/var).
	want, _ := filepath.EvalSymlinks(projRoot)
	assert.True(t, strings.Contains(string(out), want),
		"expected stdout to reference projRoot %q, got:\n%s", want, string(out))
}

// TestConfigPath_UntrustedProjectSkipped runs `config path` WITHOUT trusting
// projRoot, from a SUBDIR of projRoot. It captures stdout and asserts the
// project line is present but marked [untrusted, skipped] (not just exit code)
// so a broken trust-check or broken format string fails the test.
func TestConfigPath_UntrustedProjectSkipped(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	projCfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(projCfgDir, 0o755)
	os.WriteFile(filepath.Join(projCfgDir, "config.toml"), []byte("# p"), 0o600)
	// global config at home (so global: line is present)
	os.MkdirAll(filepath.Join(home, ".config", "mysql-cli"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "mysql-cli", "config.toml"), []byte("# g"), 0o600)
	// chdir into a SUBDIR of projRoot so DiscoverProject must walk up; if
	// discovery were broken no "project:" line would appear, failing the
	// [untrusted, skipped] assertion below.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)

	// Capture os.Stdout with pre-registered t.Cleanup (panic-safe).
	orig := os.Stdout
	r, w, _ := os.Pipe()
	t.Cleanup(func() { os.Stdout = orig; r.Close() })
	os.Stdout = w
	code := Run([]string{"config", "path"})
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)

	assert.Equal(t, ExitOK, code)
	// Tag assertions distinguish: discovery worked (project: present), trust
	// was NOT recorded ([untrusted, skipped] vs [trusted]), global still listed.
	assert.Contains(t, string(out), "project:")
	assert.Contains(t, string(out), "[untrusted, skipped]")
	assert.Contains(t, string(out), "global:")
}

// TestConfigShow_MasksPassword verifies the security-critical masking
// behavior of `config show` (plaintext -> ***, ${ENV} placeholder preserved
// as-is). It strengthens the brief's test (which only asserted ExitOK) by
// capturing stdout and asserting the actual masked content - so a regression
// that leaks a plaintext password, drops a placeholder, or fails to mask would
// fail this test.
//
// Setup: a GLOBAL config (under home, always trusted) with two datasources -
//   - "plain" with password = "supersecret" (must be masked to "***")
//   - "env"   with password = "${MYSQL_PW}" (must be printed AS-IS)
//
// The test runs `config show` (text mode) AND `config show -j` (JSON mode),
// asserting for each: contains "***", contains "${MYSQL_PW}", does NOT contain
// "supersecret". cwd is restored via t.Cleanup; os.Stdout capture registers
// t.Cleanup BEFORE mutating os.Stdout (panic-safe per existing tests).
func TestConfigShow_MasksPassword(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Global config under home: always loaded (kind=global, Trusted=true).
	cfgDir := filepath.Join(home, ".config", "mysql-cli")
	assert.NoError(t, os.MkdirAll(cfgDir, 0o755))
	assert.NoError(t, os.WriteFile(
		filepath.Join(cfgDir, "config.toml"),
		[]byte(`default = "plain"
default_limit = 1000

[datasource.plain]
host = "h1"
user = "u1"
password = "supersecret"
database = "db1"

[datasource.env]
host = "h2"
user = "u2"
password = "${MYSQL_PW}"
database = "db2"
`), 0o600))
	// chdir into home so project discovery finds no project (only global loads).
	os.Chdir(home)

	// Capture os.Stdout (config show writes via cmd.OutOrStdout() -> os.Stdout).
	// Pre-register t.Cleanup BEFORE mutating os.Stdout so a panic between the
	// assignment and a later Cleanup registration cannot leak the pipe-writer.
	capture := func(args []string) (int, string) {
		orig := os.Stdout
		r, w, _ := os.Pipe()
		t.Cleanup(func() { os.Stdout = orig; r.Close() })
		os.Stdout = w
		code := Run(args)
		// restore + drain BEFORE returning so subsequent captures see a clean state
		os.Stdout = orig
		w.Close()
		out, _ := io.ReadAll(r)
		// r.Close is deferred to t.Cleanup (already registered) - but to be tidy
		// we already returned; the registered Cleanup will close r.
		return code, string(out)
	}

	// Text mode: password MUST be masked, placeholder printed as-is, plaintext
	// MUST NOT leak into stdout.
	code, out := capture([]string{"config", "show"})
	assert.Equal(t, ExitOK, code)
	assert.Contains(t, out, "***", "plaintext password should be masked to ***")
	assert.Contains(t, out, "${MYSQL_PW}", "${ENV} placeholder should be printed as-is")
	assert.NotContains(t, out, "supersecret", "plaintext password MUST NOT leak to stdout")

	// JSON mode: same masking guarantees. Belt-and-suspenders: parse the
	// envelope and verify the password field values are exactly *** and ${MYSQL_PW}.
	code, out = capture([]string{"config", "show", "-j"})
	assert.Equal(t, ExitOK, code)
	assert.Contains(t, out, "***")
	assert.Contains(t, out, "${MYSQL_PW}")
	assert.NotContains(t, out, "supersecret")
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Default      string `json:"default"`
			DefaultLimit int    `json:"default_limit"`
			Datasources  map[string]struct {
				Password string `json:"password"`
				Host     string `json:"host"`
			} `json:"datasources"`
		} `json:"data"`
	}
	assert.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.True(t, env.Success)
	assert.Equal(t, "plain", env.Data.Default)
	assert.Equal(t, 1000, env.Data.DefaultLimit)
	if assert.Contains(t, env.Data.Datasources, "plain") {
		assert.Equal(t, "***", env.Data.Datasources["plain"].Password)
		assert.Equal(t, "h1", env.Data.Datasources["plain"].Host)
	}
	if assert.Contains(t, env.Data.Datasources, "env") {
		assert.Equal(t, "${MYSQL_PW}", env.Data.Datasources["env"].Password)
		assert.Equal(t, "h2", env.Data.Datasources["env"].Host)
	}
}

// TestConfigShow_SingleDatasource verifies `config show <name>` filters to one
// datasource and still masks. Strengthens the brief's test (which only used -j
// and asserted ExitOK) by capturing stdout and asserting both the masking and
// the filter (only the requested datasource is shown).
func TestConfigShow_SingleDatasource(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "mysql-cli")
	assert.NoError(t, os.MkdirAll(cfgDir, 0o755))
	assert.NoError(t, os.WriteFile(
		filepath.Join(cfgDir, "config.toml"),
		[]byte(`default = "a"
[datasource.a]
host = "ha"
password = "pw-a"
[datasource.b]
host = "hb"
password = "pw-b"
`), 0o600))
	os.Chdir(home)

	orig := os.Stdout
	r, w, _ := os.Pipe()
	t.Cleanup(func() { os.Stdout = orig; r.Close() })
	os.Stdout = w
	code := Run([]string{"config", "show", "a"})
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)

	assert.Equal(t, ExitOK, code)
	s := string(out)
	assert.Contains(t, s, "***")
	assert.NotContains(t, s, "pw-a")
	assert.NotContains(t, s, "pw-b")
	assert.NotContains(t, s, "datasource.b:")
	assert.Contains(t, s, "datasource.a:")
}

// TestConfigInit_ProjectCreatesFile verifies `config init --project` writes the
// template to <cwd>/.config/mysql-cli/config.toml. Strengthens the brief's test
// by restoring cwd via t.Cleanup AND asserting the file content actually
// contains "default" (not just that the file exists) - so a regression that
// writes an empty file or writes to the wrong path fails the test.
func TestConfigInit_ProjectCreatesFile(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	assert.NoError(t, os.MkdirAll(projRoot, 0o755))
	assert.NoError(t, os.Chdir(projRoot))
	assert.Equal(t, ExitOK, Run([]string{"config", "init", "--project"}))
	path := filepath.Join(projRoot, ".config", "mysql-cli", "config.toml")
	_, err := os.Stat(path)
	assert.NoError(t, err, "config file should exist at %s", path)
	b, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Contains(t, string(b), "default", "template should contain 'default'")
}

// TestConfigInit_DoesNotOverwrite verifies the --force gate: without --force an
// existing config is left EXACTLY untouched (exact-equality assertion, not
// substring - so any byte change would fail); with --force the file is replaced
// by the template (content no longer equals "# existing").
func TestConfigInit_DoesNotOverwrite(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	gp := filepath.Join(home, ".config", "mysql-cli", "config.toml")
	assert.NoError(t, os.MkdirAll(filepath.Dir(gp), 0o755))
	assert.NoError(t, os.WriteFile(gp, []byte("# existing"), 0o600))
	// without --force -> non-zero exit, file EXACTLY unchanged
	code := Run([]string{"config", "init", "--global"})
	assert.NotEqual(t, ExitOK, code)
	b, err := os.ReadFile(gp)
	assert.NoError(t, err)
	assert.Equal(t, "# existing", string(b)) // exact equality, NOT substring
	// with --force -> overwritten, content no longer "# existing"
	assert.Equal(t, ExitOK, Run([]string{"config", "init", "--global", "--force"}))
	b2, err := os.ReadFile(gp)
	assert.NoError(t, err)
	assert.NotEqual(t, "# existing", string(b2))
}

// TestConfigShow_UnknownDatasource verifies the error path for an unknown name.
func TestConfigShow_UnknownDatasource(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "mysql-cli")
	assert.NoError(t, os.MkdirAll(cfgDir, 0o755))
	assert.NoError(t, os.WriteFile(
		filepath.Join(cfgDir, "config.toml"),
		[]byte(`default = "a"
[datasource.a]
host = "ha"
`), 0o600))
	os.Chdir(home)

	assert.NotEqual(t, ExitOK, Run([]string{"config", "show", "nope"}))
}
