package cli

import "github.com/spf13/cobra"

// newConfigCmd is a placeholder. Subcommands (list/trust/untrust/show) are
// wired in later tasks (Task 7+); this stub only registers the parent
// "config" command so the help tree is complete and PersistentPreRunE
// (which sets Globals.ConfigExplicit) runs on its subcommands.
func newConfigCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Manage config: project-level discovery, trust, and inspection",
	}
}
