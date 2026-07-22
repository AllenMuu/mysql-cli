package config

import (
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
	os.Setenv("MYSQL_PROD_PASSWORD", "envpw")
	defer os.Unsetenv("MYSQL_PROD_PASSWORD")
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
	os.Setenv("MYSQL_HOST", "envhost")
	os.Setenv("MYSQL_PORT", "3307")
	os.Setenv("MYSQL_USER", "envuser")
	os.Setenv("MYSQL_PASSWORD", "envpass")
	os.Setenv("MYSQL_DATABASE", "envdb")
	defer func() {
		os.Unsetenv("MYSQL_HOST")
		os.Unsetenv("MYSQL_PORT")
		os.Unsetenv("MYSQL_USER")
		os.Unsetenv("MYSQL_PASSWORD")
		os.Unsetenv("MYSQL_DATABASE")
	}()
	ds := FromEnv()
	assert.Equal(t, "envhost", ds.Host)
	assert.Equal(t, 3307, ds.Port)
	assert.Equal(t, "envuser", ds.User)
	assert.Equal(t, "envdb", ds.Database)
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
	assert.Error(t, err)
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
