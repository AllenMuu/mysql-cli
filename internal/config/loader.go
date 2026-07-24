package config

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// relConfigPath is the shared relative path for both global and project configs.
const relConfigPath = ".config/mysql-cli/config.toml"

// DiscoverProject walks up from start looking for .config/mysql-cli/config.toml.
// Returns (projectRoot, configPath, found). projectRoot strips the relConfigPath
// suffix (it is the dir containing .config/, NOT .config/mysql-cli/ itself).
// Stops when reaching home or the filesystem root.
func DiscoverProject(start, home string) (root, configPath string, found bool) {
	dir := start
	for {
		// stop at home boundary FIRST (home is never a project root): project
		// and global configs share relConfigPath, so checking home's candidate
		// before the boundary would wrongly treat the global config as a project.
		if dir == home || dir == filepath.Dir(dir) {
			return "", "", false
		}
		candidate := filepath.Join(dir, relConfigPath)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return dir, candidate, true
		}
		dir = filepath.Dir(dir)
	}
}

// MergeConfigs overlays high onto low using覆盖式 (override) semantics:
// same-name datasource is replaced wholesale (including SSH subtable),
// distinct names are unioned, Default/DefaultLimit override when non-zero/non-empty.
// high==nil returns low unchanged (nil-safe).
// PathEntry is one resolved config file in the chain (diagnostic view).
type PathEntry struct {
	Path    string // absolute config file path
	Kind    string // "explicit" | "project" | "global"
	Trusted bool   // true for explicit/global; project-only signal
	Exists  bool   // file present on disk
}

// LoadOpts controls path resolution, project discovery, and trust checks.
type LoadOpts struct {
	ConfigFlag string                        // --config value ("" if not explicitly set)
	EnvConfig  string                        // MYSQL_CLI_CONFIG value ("" if unset)
	Cwd        string                        // project discovery start dir
	Home       string                        // home dir: global config + trust store
	IsTrusted  func(projectRoot string) bool // injectable; nil -> always false (Phase 1)
}

// globalConfigPath returns <home>/.config/mysql-cli/config.toml.
func globalConfigPath(home string) string { return filepath.Join(home, relConfigPath) }

// ResolvePathChain returns the diagnostic view of all discovered entries
// (including an untrusted project entry marked Trusted=false), ordered low->high.
func ResolvePathChain(opts LoadOpts) ([]PathEntry, error) {
	var entries []PathEntry
	// explicit single-file (flag or env) short-circuits discovery
	if opts.ConfigFlag != "" || opts.EnvConfig != "" {
		p := opts.ConfigFlag
		if p == "" {
			p = opts.EnvConfig
		}
		_, err := os.Stat(p)
		entries = []PathEntry{{Path: p, Kind: "explicit", Trusted: true, Exists: err == nil}}
		return entries, nil
	}
	// global first (low priority), then project (higher priority)
	gp := globalConfigPath(opts.Home)
	_, err := os.Stat(gp)
	entries = append(entries, PathEntry{Path: gp, Kind: "global", Trusted: true, Exists: err == nil})
	if root, p, found := DiscoverProject(opts.Cwd, opts.Home); found {
		trusted := false
		if opts.IsTrusted != nil {
			trusted = opts.IsTrusted(root)
		}
		entries = append(entries, PathEntry{Path: p, Kind: "project", Trusted: trusted, Exists: true})
	}
	return entries, nil
}

// Load resolves the chain, loads trusted/explicit/global entries, merges -> Config.
// Returns (mergedConfig, entries, err). mergedConfig is nil if no file was loaded.
// Trust is enforced at merge time: an untrusted project entry is NOT loaded,
// so the merged Config contains only trusted sources.
func Load(opts LoadOpts) (*Config, []PathEntry, error) {
	isTrusted := opts.IsTrusted
	if isTrusted == nil {
		isTrusted = func(root string) bool { return IsTrusted(opts.Home, root) }
	}
	opts.IsTrusted = isTrusted
	entries, err := ResolvePathChain(opts)
	if err != nil {
		return nil, entries, err
	}
	var merged *Config
	// load low->high: global first, then project (if trusted). explicit is single.
	for _, e := range entries {
		if !e.Exists {
			continue
		}
		if e.Kind == "project" && !e.Trusted {
			continue // untrusted project: skip load entirely
		}
		cfg, err := LoadFile(e.Path)
		if err != nil {
			return nil, entries, err
		}
		merged = MergeConfigs(merged, cfg)
	}
	return merged, entries, nil
}

func MergeConfigs(low, high *Config) *Config {
	if high == nil {
		return low
	}
	if low == nil {
		low = &Config{Datasources: map[string]Datasource{}}
	}
	out := &Config{
		DefaultDatasource: low.DefaultDatasource,
		DefaultLimit:      low.DefaultLimit,
		Datasources:       map[string]Datasource{},
	}
	for k, v := range low.Datasources {
		out.Datasources[k] = v
	}
	for k, v := range high.Datasources {
		out.Datasources[k] = v // whole-replace (shallow copy of Datasource value is fine: it's a value type, SSH ptr shared with high)
	}
	if high.DefaultDatasource != "" {
		out.DefaultDatasource = high.DefaultDatasource
	}
	if high.DefaultLimit != 0 {
		out.DefaultLimit = high.DefaultLimit
	}
	return out
}

// TrustFilePath returns <home>/.config/mysql-cli/trusted.
func TrustFilePath(home string) string {
	return filepath.Join(home, relConfigPath[:len(relConfigPath)-len("config.toml")]+"trusted")
}

// ReadTrusted parses the plaintext trust file (one normalized path per line).
// Missing file -> empty list, no error.
func ReadTrusted(home string) ([]string, error) {
	b, err := os.ReadFile(TrustFilePath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// normalizePath resolves symlinks to a canonical absolute path.
func normalizePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// IsTrusted reports whether projectRoot (symlink-normalized) is in the trust file.
func IsTrusted(home, projectRoot string) bool {
	target := normalizePath(projectRoot)
	list, err := ReadTrusted(home)
	if err != nil {
		return false
	}
	for _, e := range list {
		if e == target {
			return true
		}
	}
	return false
}

// AddTrust appends projectRoot (normalized) to the trust file, idempotently.
// Creates the parent dir and file with 0600 if absent.
func AddTrust(home, projectRoot string) error {
	target := normalizePath(projectRoot)
	list, _ := ReadTrusted(home)
	for _, e := range list {
		if e == target {
			return nil
		}
	}
	list = append(list, target)
	sort.Strings(list)
	var b strings.Builder
	for i, e := range list {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e)
	}
	b.WriteByte('\n')
	tf := TrustFilePath(home)
	if err := os.MkdirAll(filepath.Dir(tf), 0o700); err != nil {
		return err
	}
	return os.WriteFile(tf, []byte(b.String()), 0o600)
}

// placeholderMaskRe matches ${ENV} password placeholders (reuse shape from config.go).
var placeholderMaskRe = regexp.MustCompile(`^\$\{[A-Z_][A-Z0-9_]*\}$`)

// Masked returns a copy of ds with a plaintext password replaced by "***".
// "${ENV}" placeholders (and empty) are left unchanged.
func Masked(ds Datasource) Datasource {
	out := ds // value copy
	if ds.Password != "" && !placeholderMaskRe.MatchString(ds.Password) {
		out.Password = "***"
	}
	return out
}