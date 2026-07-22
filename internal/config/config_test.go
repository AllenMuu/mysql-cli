package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(`
default = "dev"

[datasource.dev]
host = "127.0.0.1"
port = 3306
user = "root"
password = "secret"
database = "test"

[datasource.prod]
host = "db.prod"
port = 3306
user = "ro"
password = "${MYSQL_PROD_PASSWORD}"
`), 0o600)
	assert.NoError(t, err)

	cfg, err := LoadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, "dev", cfg.DefaultDatasource)
	assert.Equal(t, "127.0.0.1", cfg.Datasources["dev"].Host)
	assert.Equal(t, 3306, cfg.Datasources["dev"].Port)
	assert.Equal(t, "db.prod", cfg.Datasources["prod"].Host)
}

func TestPasswordEnvPlaceholder(t *testing.T) {
	t.Setenv("MYSQL_PROD_PASSWORD", "envpw")
	cfg, _ := LoadFile(writeTmp(t, `
[datasource.prod]
host = "db"
password = "${MYSQL_PROD_PASSWORD}"
`))
	expanded, err := Resolve(cfg, "prod", Datasource{})
	assert.NoError(t, err)
	assert.Equal(t, "envpw", expanded.Password)
}

func TestFromEnv(t *testing.T) {
	t.Setenv("MYSQL_HOST", "envhost")
	t.Setenv("MYSQL_PORT", "3307")
	t.Setenv("MYSQL_USER", "envuser")
	t.Setenv("MYSQL_PASSWORD", "envpass")
	t.Setenv("MYSQL_DATABASE", "envdb")
	ds := FromEnv()
	assert.Equal(t, "envhost", ds.Host)
	assert.Equal(t, 3307, ds.Port)
	assert.Equal(t, "envuser", ds.User)
	assert.Equal(t, "envpass", ds.Password)
	assert.Equal(t, "envdb", ds.Database)
	assert.Equal(t, "TRADITIONAL", ds.SQLMode)
	assert.Equal(t, "utf8mb4", ds.Charset)
}

func TestResolveOverridesWin(t *testing.T) {
	cfg, _ := LoadFile(writeTmp(t, `
[datasource.dev]
host = "fromfile"
port = 3306
`))
	over := Datasource{Host: "fromflag"}
	ds, err := Resolve(cfg, "dev", over)
	assert.NoError(t, err)
	assert.Equal(t, "fromflag", ds.Host)
	assert.Equal(t, 3306, ds.Port) // not overridden -> from file
}

func TestResolveUnknownDatasource(t *testing.T) {
	cfg, _ := LoadFile(writeTmp(t, ``))
	_, err := Resolve(cfg, "nope", Datasource{})
	assert.ErrorIs(t, err, ErrUnknownDatasource)
}

func TestEnvOverridesFile(t *testing.T) {
	cfg, _ := LoadFile(writeTmp(t, `
[datasource.dev]
host = "filehost"
port = 3306
`))
	t.Setenv("MYSQL_HOST", "envhost")
	ds, err := Resolve(cfg, "dev", Datasource{})
	assert.NoError(t, err)
	assert.Equal(t, "envhost", ds.Host)
	assert.Equal(t, 3306, ds.Port)
}

func TestDefaultsApplied(t *testing.T) {
	ds, err := Resolve(nil, "", Datasource{})
	assert.NoError(t, err)
	assert.Equal(t, "localhost", ds.Host)
	assert.Equal(t, 3306, ds.Port)
	assert.Equal(t, 10, ds.ConnectTimeout)
	assert.Equal(t, "TRADITIONAL", ds.SQLMode)
	assert.Equal(t, "utf8mb4", ds.Charset)
}

func TestMergeAllFields(t *testing.T) {
	cfg, _ := LoadFile(writeTmp(t, `
[datasource.dev]
host = "filehost"
port = 3306
connect_timeout = 5
sql_mode = "ANSI"
charset = "latin1"
collation = "latin1_swedish_ci"
auth_plugin = "mysql_native_password"
`))
	over := Datasource{
		ConnectTimeout: 30,
		SQLMode:        "TRADITIONAL",
		Charset:        "utf8mb4",
		Collation:      "utf8mb4_general_ci",
		AuthPlugin:     "caching_sha2_password",
	}
	ds, err := Resolve(cfg, "dev", over)
	assert.NoError(t, err)
	assert.Equal(t, 30, ds.ConnectTimeout)
	assert.Equal(t, "TRADITIONAL", ds.SQLMode)
	assert.Equal(t, "utf8mb4", ds.Charset)
	assert.Equal(t, "utf8mb4_general_ci", ds.Collation)
	assert.Equal(t, "caching_sha2_password", ds.AuthPlugin)
	assert.Equal(t, "filehost", ds.Host)
}

func TestPlaceholderUnsetErrors(t *testing.T) {
	os.Unsetenv("MISSING_VAR")
	cfg, _ := LoadFile(writeTmp(t, `
[datasource.dev]
host = "db"
password = "${MISSING_VAR}"
`))
	_, err := Resolve(cfg, "dev", Datasource{})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnknownDatasource) || err.Error() == "password env var MISSING_VAR is not set")
}

func writeTmp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
