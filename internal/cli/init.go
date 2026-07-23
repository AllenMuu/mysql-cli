package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	bundle "github.com/AllenMuu/mysql-cli"
	"github.com/AllenMuu/mysql-cli/internal/agents"
	"github.com/spf13/cobra"
)

// ErrInitAllFailed is returned when every selected agent install failed.
var ErrInitAllFailed = errors.New("all agent skill installs failed")

func newInitCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "init",
		Short: "Install bundled skills into detected AI agents",
		Long: "Detect installed AI agents (Claude Code, Cursor, Codex, OpenCode, " +
			"Copilot, Windsurf, Aider) and install the bundled mysql-cli skills " +
			"into each in the agent's native format. Idempotent and re-runnable.",
		Args: cobra.NoArgs,
	}
	c.Flags().String("agent", "auto", "agent selection: auto|all|comma list (claude,cursor,...)")
	c.Flags().String("project-dir", "", "project root for project-level install")
	c.Flags().Bool("no-global", false, "skip global install")
	c.Flags().Bool("dry-run", false, "report without writing files")
	c.Flags().BoolP("json", "j", false, "emit JSON instead of text")

	c.RunE = func(cmd *cobra.Command, args []string) error {
		agentSel, _ := cmd.Flags().GetString("agent")
		projectDir, _ := cmd.Flags().GetString("project-dir")
		noGlobal, _ := cmd.Flags().GetBool("no-global")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		asJSON, _ := cmd.Flags().GetBool("json")

		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home, _ = os.Getwd()
		}
		fsys, err := bundle.SkillsFS()
		if err != nil {
			return err
		}
		names, err := bundle.SkillNames()
		if err != nil {
			return err
		}
		opts := agents.Options{
			Home:       home,
			ProjectDir: projectDir,
			NoGlobal:   noGlobal,
			DryRun:     dryRun,
			FS:         fsys,
			Names:      names,
		}
		results := agents.Run(agentSel, opts)

		var emitErr error
		if asJSON {
			emitErr = emitInitJSON(cmd.OutOrStdout(), results)
		} else {
			emitInitText(cmd.OutOrStdout(), results)
		}
		if emitErr != nil {
			return emitErr
		}
		if allFailed(results) {
			return ErrInitAllFailed
		}
		return nil
	}
	return c
}

func allFailed(results []agents.InstallResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r.Status != "error" {
			return false
		}
	}
	return true
}

func emitInitText(w io.Writer, results []agents.InstallResult) {
	fmt.Fprintln(w, "🔧 mysql-cli skill init")
	for _, r := range results {
		switch r.Status {
		case "installed":
			fmt.Fprintf(w, "   ✅ %-10s %s\n", r.Agent, strings.Join(r.Paths, ", "))
		case "skipped":
			fmt.Fprintf(w, "   ⏭️  %-10s %s\n", r.Agent, r.Error)
		case "error":
			fmt.Fprintf(w, "   ❌ %-10s %s\n", r.Agent, r.Error)
		}
	}
}

func emitInitJSON(w io.Writer, results []agents.InstallResult) error {
	type envelope struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
		Error   string         `json:"error"`
	}
	env := envelope{
		Success: !allFailed(results),
		Data:    map[string]any{"agents": results},
	}
	if !env.Success {
		env.Error = "all agent skill installs failed"
	}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(out))
	return nil
}
