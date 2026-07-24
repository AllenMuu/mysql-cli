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