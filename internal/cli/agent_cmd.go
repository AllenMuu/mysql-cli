package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/AllenMuu/mysql-cli/internal/agentsetup"
	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
)

// newAgentCmd wires the "agent" parent command. Today it has one subcommand,
// "init", which installs per-agent write-confirmation configs.
func newAgentCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Install per-agent write-confirmation configs for mysql-cli",
	}
	cmd.AddCommand(newAgentInitCmd(g))
	return cmd
}

// newAgentInitCmd implements `agent init`: installs configs that force a human
// confirmation prompt before mysql-cli write operations. Interactive by default
// in a TTY (multi-select agents + scope); non-interactive via --agents and
// --project/--global.
func newAgentInitCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "init",
		Short: "Install write-confirmation configs into the agents you use",
		Long: `Install per-agent configs that force a human confirmation prompt before
mysql-cli write operations (--write/--ddl/--yes). Interactive by default in a
TTY; use --agents and --project/--global for non-interactive use.

Supported agents: ` + strings.Join(agentsetup.Names(), ", ") + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentInit(cmd)
		},
	}
	c.Flags().String("agents", "", "comma-separated agent names (non-interactive)")
	c.Flags().Bool("project", false, "write to the current project")
	c.Flags().Bool("global", false, "write to the user-level config (~/.claude, ~/.config/opencode, ...)")
	c.Flags().Bool("force", false, "overwrite single-file configs that already exist")
	c.Flags().Bool("dry-run", false, "print actions without writing")
	c.Flags().BoolP("json", "j", false, "emit JSON")
	return c
}

func runAgentInit(cmd *cobra.Command) error {
	agentsFlag, _ := cmd.Flags().GetString("agents")
	project, _ := cmd.Flags().GetBool("project")
	global, _ := cmd.Flags().GetBool("global")
	force, _ := cmd.Flags().GetBool("force")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	asJSON, _ := cmd.Flags().GetBool("json")

	if project && global {
		return errors.New("specify only one of --project or --global")
	}

	tty := stdinIsTerminal()

	// resolve agent names
	var names []string
	if agentsFlag != "" {
		for _, n := range strings.Split(agentsFlag, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	} else if tty {
		names = promptAgents()
	} else {
		return errors.New("not a TTY; pass --agents=<names> and --project/--global")
	}
	if len(names) == 0 {
		return errors.New("no agents selected")
	}
	for _, n := range names {
		if _, ok := agentsetup.Lookup(n); !ok {
			return fmt.Errorf("unknown agent %q (available: %s)", n, strings.Join(agentsetup.Names(), ", "))
		}
	}

	// resolve scope
	var scope agentsetup.Scope
	switch {
	case project:
		scope = agentsetup.ScopeProject
	case global:
		scope = agentsetup.ScopeGlobal
	default:
		if tty {
			scope = promptScope()
		} else {
			return errors.New("specify --project or --global")
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
	opts := agentsetup.InstallOpts{
		Scope:      scope,
		Force:      force,
		DryRun:     dryRun,
		ProjectDir: cwd,
		Home:       home,
	}

	type res struct {
		Agent   string   `json:"agent"`
		Written []string `json:"written,omitempty"`
		Error   string   `json:"error,omitempty"`
	}
	var results []res
	var failMsgs []string
	for _, n := range names {
		a, _ := agentsetup.Lookup(n)
		written, err := a.Install(opts)
		r := res{Agent: n, Written: written}
		if err != nil {
			r.Error = err.Error()
			failMsgs = append(failMsgs, fmt.Sprintf("%s (%s)", n, err.Error()))
		}
		results = append(results, r)
	}
	failureSummary := strings.Join(failMsgs, "; ")

	w := cmd.OutOrStdout()
	if asJSON {
		// 信封格式与 format 包对齐：任一 agent 失败时 success:false + error
		// 字段（data 保留 results 供诊断），脚本化调用方能感知失败。
		payload := map[string]any{"success": len(failMsgs) == 0, "data": map[string]any{
			"scope":   scope.String(),
			"dry_run": dryRun,
			"results": results,
		}}
		if len(failMsgs) > 0 {
			payload["error"] = map[string]any{
				"code":    "CONFIG_ERROR",
				"message": "agent init failed for: " + failureSummary,
			}
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(w, string(b))
		if len(failMsgs) > 0 {
			return fmt.Errorf("%w: agent init failed for: %s", config.ErrConfig, failureSummary)
		}
		return nil
	}
	fmt.Fprintf(w, "scope: %s%s\n", scope, dryRunSuffix(dryRun))
	for _, r := range results {
		if r.Error != "" {
			fmt.Fprintf(w, "  ❌ %s: %s\n", r.Agent, r.Error)
			continue
		}
		fmt.Fprintf(w, "  ✅ %s:\n", r.Agent)
		for _, p := range r.Written {
			fmt.Fprintf(w, "     %s\n", p)
		}
	}
	if len(failMsgs) > 0 {
		// 非 json 路径：结果里已逐项输出 ❌，这里只负责非零退出码（exit 10）。
		return fmt.Errorf("%w: agent init failed for: %s", config.ErrConfig, failureSummary)
	}
	return nil
}

func dryRunSuffix(dryRun bool) string {
	if dryRun {
		return " (dry-run)"
	}
	return ""
}

// stdinIsTerminal reports whether stdin is an interactive terminal. It is a
// variable so tests can force the non-interactive path.
//
// 不能用 os.ModeCharDevice 判断：/dev/null 也是字符设备，会被误判为 TTY，
// agent 以 `< /dev/null` 调用时即可绕过非交互防护（B5）。readline.IsTerminal
// 走 TCGETS ioctl，只有真终端才返回 true。
var stdinIsTerminal = func() bool {
	return readline.IsTerminal(int(os.Stdin.Fd()))
}

// promptAgents runs an interactive multi-select menu and returns the chosen
// agent names. Returns nil on EOF. All prompt output goes to stderr so the
// interactive flow never pollutes stdout (and --json output stays parseable).
func promptAgents() []string {
	rl, err := readline.NewEx(&readline.Config{Stdout: os.Stderr})
	if err != nil {
		return nil
	}
	defer rl.Close()
	fmt.Fprintln(os.Stderr, "Select agents to install (comma-separated numbers, e.g. 1,3,4):")
	for i, a := range agentsetup.Agents {
		fmt.Fprintf(os.Stderr, "  %d) %-10s %s [%s]\n", i+1, a.Name, a.Desc, a.Cap)
	}
	for {
		rl.SetPrompt("agents> ")
		line, err := rl.Readline()
		if err != nil {
			return nil // EOF
		}
		names := pickAgents(line)
		if len(names) > 0 {
			return names
		}
		fmt.Fprintln(os.Stderr, "  no valid selection, try again")
	}
}

// promptScope runs an interactive scope picker. Prompt output goes to stderr
// (same rationale as promptAgents).
func promptScope() agentsetup.Scope {
	rl, err := readline.NewEx(&readline.Config{Stdout: os.Stderr})
	if err != nil {
		return agentsetup.ScopeProject
	}
	defer rl.Close()
	for {
		rl.SetPrompt("scope [1=project, 2=global]> ")
		line, err := rl.Readline()
		if err != nil {
			return agentsetup.ScopeProject // EOF default
		}
		switch strings.TrimSpace(line) {
		case "1", "project":
			return agentsetup.ScopeProject
		case "2", "global":
			return agentsetup.ScopeGlobal
		}
		fmt.Fprintln(os.Stderr, "  enter 1 (project) or 2 (global)")
	}
}

// pickAgents parses "1,3,4" into agent names, ignoring out-of-range/invalid tokens.
func pickAgents(line string) []string {
	var names []string
	for _, part := range strings.Split(line, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		if n >= 1 && n <= len(agentsetup.Agents) {
			names = append(names, agentsetup.Agents[n-1].Name)
		}
	}
	return names
}
