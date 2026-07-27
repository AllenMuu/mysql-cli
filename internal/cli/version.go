package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is the binary version. It is injected at release build time via
// GoReleaser ldflags:
//
//	-X github.com/AllenMuu/mysql-cli/internal/cli.version={{.Version}}
//
// It defaults to "dev" for local `go build`/`go test` (no ldflags), so
// `mysql-cli --version` always works.
var version = "dev"

// newVersionCmd is the top-level `version` subcommand: it prints the binary
// version (injected at release build time via GoReleaser ldflags).
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the mysql-cli binary version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "mysql-cli version %s\n", version)
			return nil
		},
	}
}
