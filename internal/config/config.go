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
}

type fileConfig struct {
	Default     string                    `toml:"default"`
	Datasources map[string]fileDatasource `toml:"datasource"`
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
	Enable     bool   `toml:"enable"`
	Host       string `toml:"host"`
	Port       int    `toml:"port"`
	User       string `toml:"user"`
	KeyPath    string `toml:"key_path"`
	RemoteHost string `toml:"remote_host"`
	RemotePort int    `toml:"remote_port"`
	LocalPort  int    `toml:"local_port"`
}

var placeholderRe = regexp.MustCompile(`^\$\{([A-Z_][A-Z0-9_]*)\}$`)

// LoadFile parses a config.toml at path.
func LoadFile(path string) (*Config, error) {
	var fc fileConfig
	if _, err := toml.DecodeFile(path, &fc); err != nil {
		return nil, err
	}
	cfg := &Config{DefaultDatasource: fc.Default, Datasources: map[string]Datasource{}}
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
	return "", fmt.Errorf("password env var %s is not set", m[1])
}

// FromEnv returns a datasource from env vars (with defaults). Used for pure-env mode.
func FromEnv() Datasource {
	return applyDefaults(applyEnv(Datasource{}))
}

// Resolve: flag > env > file > default.
func Resolve(cfg *Config, name string, overrides Datasource) (Datasource, error) {
	base, err := fileBase(cfg, name)
	if err != nil {
		return Datasource{}, err
	}
	base = applyEnv(base)          // env > file (only fields env actually sets)
	base = merge(base, overrides)  // flag > env
	base = applyDefaults(base)     // default for still-zero fields
	return base, nil
}

// fileBase returns the file datasource for name (or default); zero if none.
func fileBase(cfg *Config, name string) (Datasource, error) {
	if name == "" && cfg != nil && cfg.DefaultDatasource != "" {
		name = cfg.DefaultDatasource
	}
	if name != "" {
		if cfg == nil {
			return Datasource{}, fmt.Errorf("%w: %s", ErrUnknownDatasource, name)
		}
		if ds, ok := cfg.Datasources[name]; ok {
			pw, err := expandPassword(ds.Password)
			if err != nil {
				return Datasource{}, err
			}
			ds.Password = pw
			return ds, nil
		}
		return Datasource{}, fmt.Errorf("%w: %s", ErrUnknownDatasource, name)
	}
	return Datasource{}, nil
}

// applyEnv overlays env vars that are actually set (os.LookupEnv).
func applyEnv(ds Datasource) Datasource {
	if v, ok := os.LookupEnv("MYSQL_HOST"); ok {
		ds.Host = v
	}
	if v, ok := os.LookupEnv("MYSQL_PORT"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			ds.Port = n
		}
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
		if n, err := strconv.Atoi(v); err == nil {
			ds.ConnectTimeout = n
		}
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
	return ds
}

// applyDefaults fills defaults for still-zero fields.
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
