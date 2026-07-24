package config

import (
	"os"
	"path/filepath"
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