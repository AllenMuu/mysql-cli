package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/spf13/cobra"
)

// newConfigCmd wires the "config" parent command and its subcommands.
// Task 7 adds "trust"; later tasks add path/show/init siblings.
func newConfigCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage config: project-level discovery, trust, and inspection",
	}
	cmd.AddCommand(newConfigTrustCmd(g))
	return cmd
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
	return c
}
