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
func expandPassword(pw string) string {
	m := placeholderRe.FindStringSubmatch(pw)
	if m == nil {
		return pw
	}
	return os.Getenv(m[1])
}

// FromEnv builds a datasource from MYSQL_* environment variables.
func FromEnv() Datasource {
	return Datasource{
		Host:           getenv("MYSQL_HOST", "localhost"),
		Port:           getenvInt("MYSQL_PORT", 3306),
		User:           os.Getenv("MYSQL_USER"),
		Password:       os.Getenv("MYSQL_PASSWORD"),
		Database:       os.Getenv("MYSQL_DATABASE"),
		SSLMode:        os.Getenv("MYSQL_SSL_MODE"),
		SSLCA:          os.Getenv("MYSQL_SSL_CA"),
		ConnectTimeout: getenvInt("MYSQL_CONNECT_TIMEOUT", 10),
		SQLMode:        getenv("MYSQL_SQL_MODE", "TRADITIONAL"),
		Charset:        getenv("MYSQL_CHARSET", "utf8mb4"),
		Collation:      os.Getenv("MYSQL_COLLATION"),
		AuthPlugin:     os.Getenv("MYSQL_AUTH_PLUGIN"),
	}
}

// Resolve merges by precedence: overrides > named datasource > env defaults.
// If name is empty, the Config's default is used; if none, env is used.
func Resolve(cfg *Config, name string, overrides Datasource) (Datasource, error) {
	base, err := baseFor(cfg, name)
	if err != nil {
		return Datasource{}, err
	}
	return merge(base, overrides), nil
}

func baseFor(cfg *Config, name string) (Datasource, error) {
	if name == "" {
		if cfg == nil || cfg.DefaultDatasource == "" {
			return FromEnv(), nil
		}
		name = cfg.DefaultDatasource
	}
	if cfg != nil {
		if ds, ok := cfg.Datasources[name]; ok {
			ds.Password = expandPassword(ds.Password)
			return ds, nil
		}
	}
	return Datasource{}, fmt.Errorf("%w: %s", ErrUnknownDatasource, name)
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
	if over.SSH != nil {
		out.SSH = over.SSH
	}
	return out
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return def
	}
	return n
}

// ErrUnknownDatasource is returned when a named datasource cannot be found.
var ErrUnknownDatasource = errors.New("unknown datasource")
