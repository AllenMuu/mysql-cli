package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/spf13/cobra"
)

// newConfigCmd wires the "config" parent command and its subcommands.
// Task 7 adds "trust"; Task 8 adds "path"; later tasks add show/init siblings.
func newConfigCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage config: project-level discovery, trust, and inspection",
	}
	cmd.AddCommand(newConfigTrustCmd(g), newConfigPathCmd(g), newConfigShowCmd(g), newConfigInitCmd(g))
	return cmd
}

// explicitConfigFlag returns the --config value only when it was explicitly
// set on the command line. Shared by path/show so they reflect the same
// "explicit single-file overrides discovery" semantics as Load.
func explicitConfigFlag(g *Globals) string {
	if g.ConfigExplicit {
		return g.ConfigPath
	}
	return ""
}

// newConfigPathCmd implements `config path`: prints the resolved config file
// chain (explicit/global/project) with trust status, ordered low->high.
// Text format: "<kind>: <path>   [trusted|untrusted, skipped|missing]".
// JSON format: {"success":true,"data":{"entries":[{path,kind,trusted,exists}]}}.
func newConfigPathCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "path",
		Short: "Show the resolved config file chain and trust status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("cannot determine home: %w", err)
			}
			if home == "" {
				return errors.New("cannot determine home: $HOME is empty")
			}
			cwd, _ := os.Getwd()
			entries, err := config.ResolvePathChain(config.LoadOpts{
				ConfigFlag: explicitConfigFlag(g),
				EnvConfig:  os.Getenv("MYSQL_CLI_CONFIG"),
				Cwd:        cwd,
				Home:       home,
				IsTrusted:  func(root string) bool { return config.IsTrusted(home, root) },
			})
			if err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				type entry struct {
					Path    string `json:"path"`
					Kind    string `json:"kind"`
					Trusted bool   `json:"trusted"`
					Exists  bool   `json:"exists"`
				}
				out := make([]entry, 0, len(entries))
				for _, e := range entries {
					out = append(out, entry{e.Path, e.Kind, e.Trusted, e.Exists})
				}
				b, _ := json.MarshalIndent(map[string]any{"success": true, "data": map[string]any{"entries": out}}, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				for _, e := range entries {
					status := "trusted"
					if e.Kind == "project" && !e.Trusted {
						status = "untrusted, skipped"
					}
					if !e.Exists {
						status = "missing"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s: %s   [%s]\n", e.Kind, e.Path, status)
				}
			}
			return nil
		},
	}
	c.Flags().BoolP("json", "j", false, "emit JSON")
	return c
}

// newConfigTrustCmd implements `config trust [dir]`.
//
// `dir` defaults to cwd. If dir is not itself a project root, walk up via
// config.DiscoverProject to find one; if none is found, fall back to dir as-is.
// The resolved absolute path is appended (idempotently) to the trust file.
func newConfigTrustCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "trust [dir]",
		Short: "Trust a project root so its .config/mysql-cli/config.toml is loaded",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("cannot determine home: %w", err)
			}
			if home == "" {
				return errors.New("cannot determine home: $HOME is empty")
			}
			dir := ""
			if len(args) == 1 {
				dir = args[0]
			} else {
				cwd, _ := os.Getwd()
				dir = cwd
			}
			// If dir is not itself a project root, walk up to find one.
			root, _, found := config.DiscoverProject(dir, home)
			if !found {
				root = dir // fall back to the given/cwd dir as-is
			}
			abs, err := filepath.Abs(root)
			if err != nil {
				return fmt.Errorf("cannot resolve path %q: %w", root, err)
			}
			// Trust is a security decision: it enables a project's config
			// (which may carry ${ENV} credentials). Require explicit --yes,
			// or an interactive y/N on a TTY. Non-interactive (AI) callers
			// must pass --yes; AI agents must not auto-trust.
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				if !stdinIsTerminal() {
					return errors.New("config trust enables a project's config (security decision); in non-interactive mode pass --yes to confirm. A human should decide; AI agents must not auto-trust")
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Trust %s? This loads its .config/mysql-cli/config.toml [y/N] ", abs)
				var resp string
				fmt.Scanln(&resp)
				if !strings.EqualFold(strings.TrimSpace(resp), "y") {
					return errors.New("trust cancelled")
				}
			}
			if err := config.AddTrust(home, abs); err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				out := map[string]any{"trusted": abs}
				b, _ := json.MarshalIndent(map[string]any{"success": true, "data": out}, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "✅ trusted: %s\n", abs)
			}
			return nil
		},
	}
	c.Flags().BoolP("json", "j", false, "emit JSON")
	c.Flags().Bool("yes", false, "confirm trust (required in non-interactive mode)")
	return c
}

// newConfigShowCmd implements `config show [name]`: prints the merged effective
// config with passwords masked via config.Masked (plaintext -> "***"; ${ENV}
// placeholders and empty passwords are printed AS-IS - never the plaintext).
//
// With a positional `name` argument, only that datasource is shown (error if
// unknown). Without, all datasources are shown sorted by name.
//
// Text format:
//
//	default: <default>
//	default_limit: <limit>
//
//	datasource.<name>:
//	  host: <host>
//	  port: <port>
//	  user: <user>
//	  password: <***|${ENV}|>
//	  database: <database>
//	  ... (ssl_mode, ssl_ca, connect_timeout, sql_mode, charset, collation, auth_plugin, ssh)
//
// JSON (-j): {"success":true,"data":{"default":"<default>","default_limit":<limit>,"datasources":{"<name>":{<masked fields>}}}}.
// JSON single: {"success":true,"data":{"datasource":"<name>","fields":{<masked>}}}.
func newConfigShowCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "show [name]",
		Short: "Show the merged effective config (passwords masked)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("cannot determine home: %w", err)
			}
			if home == "" {
				return errors.New("cannot determine home: $HOME is empty")
			}
			cwd, _ := os.Getwd()
			merged, entries, err := config.Load(config.LoadOpts{
				ConfigFlag: explicitConfigFlag(g),
				EnvConfig:  os.Getenv("MYSQL_CLI_CONFIG"),
				Cwd:        cwd,
				Home:       home,
				IsTrusted:  func(root string) bool { return config.IsTrusted(home, root) },
			})
			if err != nil {
				return err
			}
			if err := g.warnUntrustedProject(entries, merged); err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if merged == nil {
				merged = &config.Config{Datasources: map[string]config.Datasource{}}
			}
			if len(args) == 1 {
				name := args[0]
				ds, ok := merged.Datasources[name]
				if !ok {
					return fmt.Errorf("unknown datasource %q", name)
				}
				emitMaskedDS(cmd.OutOrStdout(), name, ds, asJSON)
				return nil
			}
			emitMaskedConfig(cmd.OutOrStdout(), merged, asJSON)
			return nil
		},
	}
	c.Flags().BoolP("json", "j", false, "emit JSON")
	return c
}

// configTemplate is the skeleton config.toml written by `config init`.
// It ships a single "dev" datasource with the common fields filled in;
// password is left commented out (plaintext + ${ENV} placeholder variants).
const configTemplate = `# mysql-cli config (generated by 'mysql-cli config init')
default = "dev"

[datasource.dev]
host = "127.0.0.1"
port = 3306
user = "root"
# password = "secret"            # plaintext
# password = "${MYSQL_PASSWORD}" # or ${ENV} placeholder (trusted dirs only)
database = "test"
`

// newConfigInitCmd implements `config init`: writes configTemplate to either
// <cwd>/.config/mysql-cli/config.toml (--project) or
// ~/.config/mysql-cli/config.toml (--global). Exactly one of --project/--global
// is required. An existing file is left untouched unless --force is given.
// Parent dir is created with 0700; the file is written with 0600.
func newConfigInitCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "init",
		Short: "Write a template config.toml (--project or --global)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			project, _ := cmd.Flags().GetBool("project")
			global, _ := cmd.Flags().GetBool("global")
			if project == global {
				return errors.New("specify exactly one of --project or --global")
			}
			var target string
			if global {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("cannot determine home: %w", err)
				}
				if home == "" {
					return errors.New("cannot determine home: $HOME is empty")
				}
				target = filepath.Join(home, config.RelConfigPath)
			} else {
				cwd, _ := os.Getwd()
				target = filepath.Join(cwd, config.RelConfigPath)
			}
			if !force {
				if _, err := os.Stat(target); err == nil {
					return fmt.Errorf("config already exists at %s (use --force to overwrite)", target)
				}
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(target, []byte(configTemplate), 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✅ wrote %s\n", target)
			return nil
		},
	}
	c.Flags().Bool("project", false, "write to <cwd>/.config/mysql-cli/config.toml")
	c.Flags().Bool("global", false, "write to ~/.config/mysql-cli/config.toml")
	c.Flags().Bool("force", false, "overwrite if exists")
	return c
}

// emitMaskedConfig prints the full merged config with all datasource passwords
// masked. Datasources are emitted in sorted name order for deterministic output.
func emitMaskedConfig(w io.Writer, cfg *config.Config, asJSON bool) {
	if asJSON {
		out := make(map[string]maskedDSJSON, len(cfg.Datasources))
		for name, ds := range cfg.Datasources {
			out[name] = toMaskedDSJSON(config.Masked(ds))
		}
		payload := map[string]any{
			"success": true,
			"data": map[string]any{
				"default":       cfg.DefaultDatasource,
				"default_limit": cfg.DefaultLimit,
				"datasources":   out,
			},
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintf(w, "default: %s\n", cfg.DefaultDatasource)
	fmt.Fprintf(w, "default_limit: %d\n", cfg.DefaultLimit)
	names := make([]string, 0, len(cfg.Datasources))
	for n := range cfg.Datasources {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintln(w)
		emitMaskedDS(w, name, cfg.Datasources[name], false)
	}
}

// emitMaskedDS prints a single datasource with password masked. In text mode
// it prints `datasource.<name>:` then indented fields. In JSON mode it emits
// {"success":true,"data":{"datasource":"<name>","fields":{<masked>}}}.
func emitMaskedDS(w io.Writer, name string, ds config.Datasource, asJSON bool) {
	m := config.Masked(ds)
	if asJSON {
		payload := map[string]any{
			"success": true,
			"data": map[string]any{
				"datasource": name,
				"fields":     toMaskedDSJSON(m),
			},
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintf(w, "datasource.%s:\n", name)
	fmt.Fprintf(w, "  host: %s\n", m.Host)
	fmt.Fprintf(w, "  port: %d\n", m.Port)
	fmt.Fprintf(w, "  user: %s\n", m.User)
	fmt.Fprintf(w, "  password: %s\n", m.Password)
	fmt.Fprintf(w, "  database: %s\n", m.Database)
	fmt.Fprintf(w, "  ssl_mode: %s\n", m.SSLMode)
	fmt.Fprintf(w, "  ssl_ca: %s\n", m.SSLCA)
	fmt.Fprintf(w, "  connect_timeout: %d\n", m.ConnectTimeout)
	fmt.Fprintf(w, "  sql_mode: %s\n", m.SQLMode)
	fmt.Fprintf(w, "  charset: %s\n", m.Charset)
	fmt.Fprintf(w, "  collation: %s\n", m.Collation)
	fmt.Fprintf(w, "  auth_plugin: %s\n", m.AuthPlugin)
	if m.SSH != nil {
		fmt.Fprintf(w, "  ssh:\n")
		fmt.Fprintf(w, "    enable: %t\n", m.SSH.Enable)
		fmt.Fprintf(w, "    host: %s\n", m.SSH.Host)
		fmt.Fprintf(w, "    port: %d\n", m.SSH.Port)
		fmt.Fprintf(w, "    user: %s\n", m.SSH.User)
		fmt.Fprintf(w, "    key_path: %s\n", m.SSH.KeyPath)
		fmt.Fprintf(w, "    remote_host: %s\n", m.SSH.RemoteHost)
		fmt.Fprintf(w, "    remote_port: %d\n", m.SSH.RemotePort)
		fmt.Fprintf(w, "    local_port: %d\n", m.SSH.LocalPort)
	}
}

// maskedSSHJSON is the JSON view of config.SSHConfig (snake_case keys).
type maskedSSHJSON struct {
	Enable     bool   `json:"enable"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	KeyPath    string `json:"key_path"`
	RemoteHost string `json:"remote_host"`
	RemotePort int    `json:"remote_port"`
	LocalPort  int    `json:"local_port"`
}

// maskedDSJSON is the JSON view of a masked config.Datasource (snake_case keys,
// ssh omitted when nil). The Password field carries the already-masked value
// ("***" for plaintext, "${ENV}" as-is, "" for empty).
type maskedDSJSON struct {
	Host           string         `json:"host"`
	Port           int            `json:"port"`
	User           string         `json:"user"`
	Password       string         `json:"password"`
	Database       string         `json:"database"`
	SSLMode        string         `json:"ssl_mode"`
	SSLCA          string         `json:"ssl_ca"`
	ConnectTimeout int            `json:"connect_timeout"`
	SQLMode        string         `json:"sql_mode"`
	Charset        string         `json:"charset"`
	Collation      string         `json:"collation"`
	AuthPlugin     string         `json:"auth_plugin"`
	SSH            *maskedSSHJSON `json:"ssh,omitempty"`
}

// toMaskedDSJSON converts a (already-masked) Datasource into its JSON view.
func toMaskedDSJSON(m config.Datasource) maskedDSJSON {
	var ssh *maskedSSHJSON
	if m.SSH != nil {
		ssh = &maskedSSHJSON{
			Enable: m.SSH.Enable, Host: m.SSH.Host, Port: m.SSH.Port,
			User: m.SSH.User, KeyPath: m.SSH.KeyPath, RemoteHost: m.SSH.RemoteHost,
			RemotePort: m.SSH.RemotePort, LocalPort: m.SSH.LocalPort,
		}
	}
	return maskedDSJSON{
		Host: m.Host, Port: m.Port, User: m.User, Password: m.Password,
		Database: m.Database, SSLMode: m.SSLMode, SSLCA: m.SSLCA,
		ConnectTimeout: m.ConnectTimeout, SQLMode: m.SQLMode,
		Charset: m.Charset, Collation: m.Collation, AuthPlugin: m.AuthPlugin,
		SSH: ssh,
	}
}
