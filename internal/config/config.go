// Package config loads named datasources from config.toml, builds a
// datasource from MYSQL_* environment variables (compatible with the
// original MCP), and resolves a final datasource with precedence:
// CLI overrides > env > file > default.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/BurntSushi/toml"
)

// SSHConfig mirrors the original MCP's MYSQL_SSH_* options.
type SSHConfig struct {
	Enable     bool
	Host       string
	Port       int
	User       string
	KeyPath    string
	RemoteHost string
	RemotePort int
	LocalPort  int
	// KnownHostsFile 是 known_hosts 文件路径；空表示用默认 ~/.ssh/known_hosts。
	KnownHostsFile string
	// InsecureIgnoreHostKey=true 时跳过 host key 校验（MITM 风险，仅调试用）。
	InsecureIgnoreHostKey bool
}

// Datasource is a single MySQL connection target.
type Datasource struct {
	Host           string
	Port           int
	User           string
	Password       string
	Database       string
	SSLMode        string
	SSLCA          string
	ConnectTimeout int
	SQLMode        string
	Charset        string
	Collation      string
	AuthPlugin     string
	SSH            *SSHConfig
}

// Config is the parsed set of named datasources.
type Config struct {
	Datasources       map[string]Datasource `toml:"datasource"`
	DefaultDatasource string                `toml:"default"`
	DefaultLimit      int                   `toml:"default_limit"`
}

type fileConfig struct {
	Default      string                    `toml:"default"`
	DefaultLimit int                       `toml:"default_limit"`
	Datasources  map[string]fileDatasource `toml:"datasource"`
}

type fileDatasource struct {
	Host           string   `toml:"host"`
	Port           int      `toml:"port"`
	User           string   `toml:"user"`
	Password       string   `toml:"password"`
	Database       string   `toml:"database"`
	SSLMode        string   `toml:"ssl_mode"`
	SSLCA          string   `toml:"ssl_ca"`
	ConnectTimeout int      `toml:"connect_timeout"`
	SQLMode        string   `toml:"sql_mode"`
	Charset        string   `toml:"charset"`
	Collation      string   `toml:"collation"`
	AuthPlugin     string   `toml:"auth_plugin"`
	SSH            *fileSSH `toml:"ssh"`
}

type fileSSH struct {
	Enable                bool   `toml:"enable"`
	Host                  string `toml:"host"`
	Port                  int    `toml:"port"`
	User                  string `toml:"user"`
	KeyPath               string `toml:"key_path"`
	RemoteHost            string `toml:"remote_host"`
	RemotePort            int    `toml:"remote_port"`
	LocalPort             int    `toml:"local_port"`
	KnownHostsFile        string `toml:"known_hosts_file"`
	InsecureIgnoreHostKey bool   `toml:"insecure_ignore_host_key"`
}

var placeholderRe = regexp.MustCompile(`^\$\{([A-Z_][A-Z0-9_]*)\}$`)

// LoadFile parses a config.toml at path.
func LoadFile(path string) (*Config, error) {
	var fc fileConfig
	if _, err := toml.DecodeFile(path, &fc); err != nil {
		// 双 %w 保链：cli 层 errors.Is(err, ErrConfig) 命中 ExitConfigError。
		return nil, fmt.Errorf("%w: %w", ErrConfig, err)
	}
	cfg := &Config{DefaultDatasource: fc.Default, DefaultLimit: fc.DefaultLimit, Datasources: map[string]Datasource{}}
	for name, fd := range fc.Datasources {
		cfg.Datasources[name] = fileToDatasource(fd)
	}
	return cfg, nil
}

func fileToDatasource(fd fileDatasource) Datasource {
	ds := Datasource{
		Host: fd.Host, Port: fd.Port, User: fd.User, Password: fd.Password,
		Database: fd.Database, SSLMode: fd.SSLMode, SSLCA: fd.SSLCA,
		ConnectTimeout: fd.ConnectTimeout, SQLMode: fd.SQLMode,
		Charset: fd.Charset, Collation: fd.Collation, AuthPlugin: fd.AuthPlugin,
	}
	if fd.SSH != nil {
		ds.SSH = &SSHConfig{
			Enable: fd.SSH.Enable, Host: fd.SSH.Host, Port: fd.SSH.Port,
			User: fd.SSH.User, KeyPath: fd.SSH.KeyPath, RemoteHost: fd.SSH.RemoteHost,
			RemotePort: fd.SSH.RemotePort, LocalPort: fd.SSH.LocalPort,
			KnownHostsFile: fd.SSH.KnownHostsFile, InsecureIgnoreHostKey: fd.SSH.InsecureIgnoreHostKey,
		}
	}
	return ds
}

// expandPassword replaces ${ENV} placeholders with the env value.
func expandPassword(pw string) (string, error) {
	m := placeholderRe.FindStringSubmatch(pw)
	if m == nil {
		return pw, nil
	}
	if v, ok := os.LookupEnv(m[1]); ok {
		return v, nil
	}
	// 双 %w：保留 ErrPlaceholderUnset 细粒度，同时挂 ErrConfig 总哨兵。
	return "", fmt.Errorf("%w: %w: %s", ErrConfig, ErrPlaceholderUnset, m[1])
}

// FromEnv returns a datasource from env vars (with defaults). Used for pure-env mode.
func FromEnv() (Datasource, error) {
	ds, err := applyEnv(Datasource{})
	if err != nil {
		return Datasource{}, err
	}
	return applyDefaults(ds), nil
}

// Resolve: flag > env > file > default.
func Resolve(cfg *Config, name string, overrides Datasource) (Datasource, error) {
	base, err := fileBase(cfg, name)
	if err != nil {
		return Datasource{}, err
	}
	base, err = applyEnv(base) // env > file (only fields env actually sets)
	if err != nil {
		return Datasource{}, err
	}
	base = merge(base, overrides) // flag > env
	base = applyDefaults(base)    // default for still-zero fields
	return base, nil
}

// fileBase returns the file datasource for name (or default); zero if none.
func fileBase(cfg *Config, name string) (Datasource, error) {
	if name == "" && cfg != nil && cfg.DefaultDatasource != "" {
		name = cfg.DefaultDatasource
	}
	if name != "" {
		if cfg == nil {
			return Datasource{}, fmt.Errorf("%w: %w: %s", ErrConfig, ErrUnknownDatasource, name)
		}
		if ds, ok := cfg.Datasources[name]; ok {
			pw, err := expandPassword(ds.Password)
			if err != nil {
				return Datasource{}, err
			}
			ds.Password = pw
			return ds, nil
		}
		return Datasource{}, fmt.Errorf("%w: %w: %s", ErrConfig, ErrUnknownDatasource, name)
	}
	return Datasource{}, nil
}

// applyEnv overlays env vars that are actually set (os.LookupEnv).
func applyEnv(ds Datasource) (Datasource, error) {
	if v, ok := os.LookupEnv("MYSQL_HOST"); ok {
		ds.Host = v
	}
	if v, ok := os.LookupEnv("MYSQL_PORT"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Datasource{}, fmt.Errorf("%w: invalid MYSQL_PORT %q: %w", ErrConfig, v, err)
		}
		if n < 1 || n > 65535 {
			return Datasource{}, fmt.Errorf("%w: invalid MYSQL_PORT %q: must be in [1,65535]", ErrConfig, v)
		}
		ds.Port = n
	}
	if v, ok := os.LookupEnv("MYSQL_USER"); ok {
		ds.User = v
	}
	if v, ok := os.LookupEnv("MYSQL_PASSWORD"); ok {
		ds.Password = v
	}
	if v, ok := os.LookupEnv("MYSQL_DATABASE"); ok {
		ds.Database = v
	}
	if v, ok := os.LookupEnv("MYSQL_SSL_MODE"); ok {
		ds.SSLMode = v
	}
	if v, ok := os.LookupEnv("MYSQL_SSL_CA"); ok {
		ds.SSLCA = v
	}
	if v, ok := os.LookupEnv("MYSQL_CONNECT_TIMEOUT"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Datasource{}, fmt.Errorf("%w: invalid MYSQL_CONNECT_TIMEOUT %q: %w", ErrConfig, v, err)
		}
		if n <= 0 {
			return Datasource{}, fmt.Errorf("%w: invalid MYSQL_CONNECT_TIMEOUT %q: must be > 0", ErrConfig, v)
		}
		ds.ConnectTimeout = n
	}
	if v, ok := os.LookupEnv("MYSQL_SQL_MODE"); ok {
		ds.SQLMode = v
	}
	if v, ok := os.LookupEnv("MYSQL_CHARSET"); ok {
		ds.Charset = v
	}
	if v, ok := os.LookupEnv("MYSQL_COLLATION"); ok {
		ds.Collation = v
	}
	if v, ok := os.LookupEnv("MYSQL_AUTH_PLUGIN"); ok {
		ds.AuthPlugin = v
	}
	// SSH 相关 env：仅覆盖字段；若 ds.SSH 仍为 nil 但设置了任一 SSH env，则创建。
	if v, ok := os.LookupEnv("MYSQL_SSH_INSECURE_IGNORE_HOST_KEY"); ok {
		if ds.SSH == nil {
			ds.SSH = &SSHConfig{}
		}
		switch v {
		case "1", "true", "TRUE", "True", "yes", "YES":
			ds.SSH.InsecureIgnoreHostKey = true
		case "0", "false", "FALSE", "False", "no", "NO", "":
			ds.SSH.InsecureIgnoreHostKey = false
		default:
			return Datasource{}, fmt.Errorf("%w: invalid MYSQL_SSH_INSECURE_IGNORE_HOST_KEY %q", ErrConfig, v)
		}
	}
	if v, ok := os.LookupEnv("MYSQL_SSH_KNOWN_HOSTS_FILE"); ok {
		if ds.SSH == nil {
			ds.SSH = &SSHConfig{}
		}
		ds.SSH.KnownHostsFile = v
	}
	return ds, nil
}

// applyDefaults fills defaults for still-zero fields.
//
// 此函数是 Datasource 默认值的唯一真相源；conn.DSN() 和其他消费者信任入参已默认化，
// 不再各自硬编码 fallback。当前 conn.DSN() 依赖的字段及其默认值：
//   - Host    -> "localhost"
//   - Port    -> 3306
//   - ConnectTimeout -> 10（秒，>0）
//   - Charset -> "utf8mb4"
//   - SQLMode -> "TRADITIONAL"
//
// 其余字段（Collation/SSLMode/SSLCA/AuthPlugin 等）留空表示"未设置"，
// conn.DSN() 在为空时跳过对应参数，故无需在此强加默认值。
func applyDefaults(ds Datasource) Datasource {
	if ds.Host == "" {
		ds.Host = "localhost"
	}
	if ds.Port == 0 {
		ds.Port = 3306
	}
	if ds.ConnectTimeout == 0 {
		ds.ConnectTimeout = 10
	}
	if ds.SQLMode == "" {
		ds.SQLMode = "TRADITIONAL"
	}
	if ds.Charset == "" {
		ds.Charset = "utf8mb4"
	}
	// SSH 默认值：KnownHostsFile 空时回落 ~/.ssh/known_hosts（展开 home 目录）。
	if ds.SSH != nil && ds.SSH.KnownHostsFile == "" {
		if home, err := os.UserHomeDir(); err == nil {
			ds.SSH.KnownHostsFile = home + "/.ssh/known_hosts"
		}
	}
	return ds
}

// merge applies non-zero overrides onto base.
func merge(base, over Datasource) Datasource {
	out := base
	if over.Host != "" {
		out.Host = over.Host
	}
	if over.Port != 0 {
		out.Port = over.Port
	}
	if over.User != "" {
		out.User = over.User
	}
	if over.Password != "" {
		out.Password = over.Password
	}
	if over.Database != "" {
		out.Database = over.Database
	}
	if over.SSLMode != "" {
		out.SSLMode = over.SSLMode
	}
	if over.SSLCA != "" {
		out.SSLCA = over.SSLCA
	}
	if over.ConnectTimeout > 0 {
		out.ConnectTimeout = over.ConnectTimeout
	}
	if over.SQLMode != "" {
		out.SQLMode = over.SQLMode
	}
	if over.Charset != "" {
		out.Charset = over.Charset
	}
	if over.Collation != "" {
		out.Collation = over.Collation
	}
	if over.AuthPlugin != "" {
		out.AuthPlugin = over.AuthPlugin
	}
	if over.SSH != nil {
		out.SSH = over.SSH
	}
	return out
}

// ErrUnknownDatasource is returned when a named datasource cannot be found.
var ErrUnknownDatasource = errors.New("unknown datasource")

// ErrPlaceholderUnset is returned when a password ${ENV} placeholder references an unset env var.
var ErrPlaceholderUnset = errors.New("placeholder env var unset")

// ErrConfig 哨兵 error：config 解析/加载失败统一挂它，cli 层 mapError
// 用 errors.Is 精确命中 ExitConfigError，取代兜底的字符串匹配。
// 已有的 ErrUnknownDatasource / ErrPlaceholderUnset 仍保留，作为更细粒度的
// 子哨兵；这里只追加一个总哨兵，不改既有解析逻辑。
var ErrConfig = errors.New("config: invalid")
