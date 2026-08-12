package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
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
	ds, err := FromEnv()
	assert.NoError(t, err)
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

// TestErrConfigSentinelWrapped（任务 2）：config 解析失败的各路径都应挂
// ErrConfig 总哨兵，让 cli 层 mapError 用 errors.Is 精确命中 ExitConfigError。
// 同时保留 ErrUnknownDatasource / ErrPlaceholderUnset 细粒度子哨兵。
func TestErrConfigSentinelWrapped(t *testing.T) {
	// 1) unknown datasource 路径：同时挂 ErrConfig 和 ErrUnknownDatasource。
	cfg, _ := LoadFile(writeTmp(t, ``))
	_, err := Resolve(cfg, "nope", Datasource{})
	assert.ErrorIs(t, err, ErrConfig)
	assert.ErrorIs(t, err, ErrUnknownDatasource)

	// 2) placeholder unset 路径：同时挂 ErrConfig 和 ErrPlaceholderUnset。
	cfg2, _ := LoadFile(writeTmp(t, `
[datasource.dev]
host = "db"
password = "${MISSING_VAR}"
`))
	_, err = Resolve(cfg2, "dev", Datasource{})
	assert.ErrorIs(t, err, ErrConfig)
	assert.ErrorIs(t, err, ErrPlaceholderUnset)

	// 3) LoadFile toml 解析失败路径：挂 ErrConfig。
	_, err = LoadFile("/no/such/file.toml")
	assert.ErrorIs(t, err, ErrConfig)

	// 4) applyEnv MYSQL_PORT 非法路径：挂 ErrConfig。
	t.Setenv("MYSQL_PORT", "abc")
	_, err = Resolve(nil, "", Datasource{})
	assert.ErrorIs(t, err, ErrConfig)
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
	cfg, _ := LoadFile(writeTmp(t, `
[datasource.dev]
host = "db"
password = "${MISSING_VAR}"
`))
	_, err := Resolve(cfg, "dev", Datasource{})
	assert.ErrorIs(t, err, ErrPlaceholderUnset)
	assert.False(t, errors.Is(err, ErrUnknownDatasource))
}

func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("MYSQL_HOST", "envhost")
	ds, err := Resolve(nil, "", Datasource{Host: "flaghost"})
	assert.NoError(t, err)
	assert.Equal(t, "flaghost", ds.Host)
}

func TestEnvInvalidPortErrors(t *testing.T) {
	t.Setenv("MYSQL_PORT", "abc")
	_, err := Resolve(nil, "", Datasource{})
	assert.ErrorIs(t, err, strconv.ErrSyntax)
	assert.ErrorContains(t, err, "invalid MYSQL_PORT")
}

func TestEnvPortOutOfRange(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		t.Setenv("MYSQL_PORT", "0")
		_, err := Resolve(nil, "", Datasource{})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "invalid MYSQL_PORT")
		assert.ErrorContains(t, err, "[1,65535]")
	})
	t.Run("negative", func(t *testing.T) {
		t.Setenv("MYSQL_PORT", "-1")
		_, err := Resolve(nil, "", Datasource{})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "invalid MYSQL_PORT")
	})
	t.Run("too_large", func(t *testing.T) {
		t.Setenv("MYSQL_PORT", "70000")
		_, err := Resolve(nil, "", Datasource{})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "invalid MYSQL_PORT")
	})
}

func TestEnvPortValid(t *testing.T) {
	t.Setenv("MYSQL_PORT", "3306")
	ds, err := Resolve(nil, "", Datasource{})
	assert.NoError(t, err)
	assert.Equal(t, 3306, ds.Port)
}

func TestEnvInvalidConnectTimeoutErrors(t *testing.T) {
	t.Setenv("MYSQL_CONNECT_TIMEOUT", "xyz")
	_, err := Resolve(nil, "", Datasource{})
	assert.ErrorIs(t, err, strconv.ErrSyntax)
	assert.ErrorContains(t, err, "invalid MYSQL_CONNECT_TIMEOUT")
}

func TestEnvConnectTimeoutNonPositive(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		t.Setenv("MYSQL_CONNECT_TIMEOUT", "0")
		_, err := Resolve(nil, "", Datasource{})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "invalid MYSQL_CONNECT_TIMEOUT")
		assert.ErrorContains(t, err, "must be > 0")
	})
	t.Run("negative", func(t *testing.T) {
		t.Setenv("MYSQL_CONNECT_TIMEOUT", "-5")
		_, err := Resolve(nil, "", Datasource{})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "invalid MYSQL_CONNECT_TIMEOUT")
	})
}

// TestApplyDefaultsCoversConnDeps 验证 applyDefaults 覆盖 conn.DSN() 依赖的所有字段。
// 详见 applyDefaults 注释里"唯一真相源"契约。
func TestApplyDefaultsCoversConnDeps(t *testing.T) {
	ds := applyDefaults(Datasource{})
	// Host/Port/ConnectTimeout/Charset/SQLMode 必须有合理默认值。
	assert.NotEmpty(t, ds.Host, "Host 必须有默认值")
	assert.Greater(t, ds.Port, 0, "Port 必须有正默认值")
	assert.LessOrEqual(t, ds.Port, 65535, "Port 默认值必须在合法范围")
	assert.Greater(t, ds.ConnectTimeout, 0, "ConnectTimeout 必须有正默认值")
	assert.NotEmpty(t, ds.Charset, "Charset 必须有默认值")
	assert.NotEmpty(t, ds.SQLMode, "SQLMode 必须有默认值")
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

func TestDefaultLimitFromConfig(t *testing.T) {
	toml := `
default = "dev"
default_limit = 2500

[datasource.dev]
host = "127.0.0.1"
port = 3306
`
	tmp := t.TempDir() + "/config.toml"
	assert.NoError(t, os.WriteFile(tmp, []byte(toml), 0644))
	cfg, err := LoadFile(tmp)
	assert.NoError(t, err)
	assert.Equal(t, 2500, cfg.DefaultLimit)
}

func TestDefaultLimitZeroWhenUnset(t *testing.T) {
	toml := `
default = "dev"
[datasource.dev]
host = "127.0.0.1"
`
	tmp := t.TempDir() + "/config.toml"
	assert.NoError(t, os.WriteFile(tmp, []byte(toml), 0644))
	cfg, err := LoadFile(tmp)
	assert.NoError(t, err)
	assert.Equal(t, 0, cfg.DefaultLimit)
}

func TestSSHHostKeyFieldsFromToml(t *testing.T) {
	toml := `
[datasource.bastion]
host = "db"
[datasource.bastion.ssh]
enable = true
host = "bastion"
known_hosts_file = "/tmp/known_hosts"
insecure_ignore_host_key = true
`
	tmp := t.TempDir() + "/config.toml"
	assert.NoError(t, os.WriteFile(tmp, []byte(toml), 0644))
	cfg, err := LoadFile(tmp)
	assert.NoError(t, err)
	ssh := cfg.Datasources["bastion"].SSH
	assert.NotNil(t, ssh)
	assert.Equal(t, "/tmp/known_hosts", ssh.KnownHostsFile)
	assert.True(t, ssh.InsecureIgnoreHostKey)
}

func TestSSHInsecureIgnoreHostKeyEnv(t *testing.T) {
	t.Setenv("MYSQL_SSH_INSECURE_IGNORE_HOST_KEY", "true")
	ds, err := FromEnv()
	assert.NoError(t, err)
	assert.NotNil(t, ds.SSH)
	assert.True(t, ds.SSH.InsecureIgnoreHostKey)
}

func TestSSHInsecureIgnoreHostKeyEnvInvalid(t *testing.T) {
	t.Setenv("MYSQL_SSH_INSECURE_IGNORE_HOST_KEY", "maybe")
	_, err := FromEnv()
	assert.Error(t, err)
	assert.ErrorContains(t, err, "invalid MYSQL_SSH_INSECURE_IGNORE_HOST_KEY")
}

func TestSSHKnownHostsFileEnv(t *testing.T) {
	t.Setenv("MYSQL_SSH_KNOWN_HOSTS_FILE", "/custom/known_hosts")
	ds, err := FromEnv()
	assert.NoError(t, err)
	assert.NotNil(t, ds.SSH)
	assert.Equal(t, "/custom/known_hosts", ds.SSH.KnownHostsFile)
}

func TestSSHKnownHostsFileDefaultApplied(t *testing.T) {
	// SSH 非 nil 且 KnownHostsFile 空 -> 默认 ~/.ssh/known_hosts
	ds := Datasource{SSH: &SSHConfig{Enable: true}}
	out := applyDefaults(ds)
	assert.NotEmpty(t, out.SSH.KnownHostsFile)
	assert.Contains(t, out.SSH.KnownHostsFile, "/.ssh/known_hosts")
	assert.False(t, out.SSH.InsecureIgnoreHostKey) // 默认 false
}

func TestSSHKnownHostsFileDefaultNotAppliedWhenNil(t *testing.T) {
	// SSH 为 nil 不应被默认值创建
	ds := applyDefaults(Datasource{})
	assert.Nil(t, ds.SSH)
}

func TestSSHKnownHostsFileEnvPreservedThroughDefaults(t *testing.T) {
	// 用户显式指定的 KnownHostsFile 不被默认覆盖
	t.Setenv("MYSQL_SSH_KNOWN_HOSTS_FILE", "/explicit/known_hosts")
	ds, err := FromEnv()
	assert.NoError(t, err)
	assert.Equal(t, "/explicit/known_hosts", ds.SSH.KnownHostsFile)
}
