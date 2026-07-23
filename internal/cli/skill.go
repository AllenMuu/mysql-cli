package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	bundle "github.com/AllenMuu/mysql-cli"
	"github.com/AllenMuu/mysql-cli/internal/skillscheck"
	"github.com/spf13/cobra"
)

// newSkillCmd groups subcommands for managing the AI-agent skills bundled
// into the binary. It never touches a database.
func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage bundled AI-agent skills (list/check/install/version)",
	}
	cmd.AddCommand(
		newSkillListCmd(),
		newSkillVersionCmd(),
		newSkillCheckCmd(),
		newSkillInstallCmd(),
	)
	return cmd
}

func newSkillListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List skills bundled with this binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return printSkillVersions(cmd.OutOrStdout())
		},
	}
}

func newSkillVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print expected skill versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return printSkillVersions(cmd.OutOrStdout())
		},
	}
}

func printSkillVersions(w io.Writer) error {
	names, err := bundle.SkillNames()
	if err != nil {
		return err
	}
	for _, n := range names {
		data, err := bundle.SkillFile(n)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\t%s\n", n, skillscheck.ParseVersion(string(data)))
	}
	return nil
}

func newSkillCheckCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "check [target-dir]",
		Short: "Check installed skills against bundled versions",
		Long: "Check installed skills under target-dir (default ~/.claude/skills) " +
			"against the versions bundled with this binary. Emits a report and " +
			"always exits 0; parse the JSON status field for programmatic use.",
		Args: cobra.MaximumNArgs(1),
	}
	c.Flags().BoolP("json", "j", false, "emit JSON instead of a text table")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		target := defaultSkillsTarget()
		if len(args) == 1 {
			target = args[0]
		}
		results, err := skillscheck.Check(target)
		if err != nil {
			return err
		}
		asJSON, _ := cmd.Flags().GetBool("json")
		if asJSON {
			out, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		}
		w := cmd.OutOrStdout()
		for _, r := range results {
			ver := r.InstalledVer
			if ver == "" {
				ver = "-"
			}
			fmt.Fprintf(w, "%-16s  %-8s  installed=%-8s  expected=%-8s  %s\n",
				r.Skill, r.Status, ver, r.ExpectedVer, r.Path)
		}
		return nil
	}
	return c
}

func newSkillInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install [target-dir]",
		Short: "Install bundled skills into target-dir (default ~/.claude/skills)",
		Args:  cobra.MaximumNArgs(1),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		target := defaultSkillsTarget()
		if len(args) == 1 {
			target = args[0]
		}
		n, err := installSkills(target)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "installed %d skill(s) into %s\n", n, target)
		return nil
	}
	return c
}

func defaultSkillsTarget() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".claude", "skills")
	}
	return filepath.Join(home, ".claude", "skills")
}

// installSkills copies every bundled skill into target, preserving the
// skills/<name>/... layout. Existing files are overwritten.
func installSkills(target string) (int, error) {
	fsys, err := bundle.SkillsFS()
	if err != nil {
		return 0, err
	}
	names, err := bundle.SkillNames()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return 0, err
	}
	count := 0
	for _, name := range names {
		err := fs.WalkDir(fsys, name, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			dst := filepath.Join(target, p)
			if d.IsDir() {
				return os.MkdirAll(dst, 0o755)
			}
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			return os.WriteFile(dst, data, 0o644)
		})
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
