package cli

import "github.com/spf13/cobra"

// Command group IDs used to organize `mysql-cli --help` output. Cobra (>=1.6)
// renders grouped commands automatically when subcommands carry a GroupID that
// matches a group registered on the root via AddGroup.
const (
	groupSQL    = "sql"
	groupSchema = "schema-explore"
	groupManage = "management"
)

// applyHelpGrouping registers command groups, assigns each subcommand to a
// group, and appends an "Agent notes" section to the root help template.
//
// Grouping is cosmetic: it only affects help output, not command resolution or
// exit codes. Subcommands without a GroupID (cobra's auto-added `help` and
// `completion`) fall under "Additional Commands".
//
// Must be called after root.AddCommand so the subcommand set is populated.
func applyHelpGrouping(root *cobra.Command) {
	root.AddGroup(
		&cobra.Group{ID: groupSQL, Title: "SQL Commands:"},
		&cobra.Group{ID: groupSchema, Title: "Schema Exploration Commands:"},
		&cobra.Group{ID: groupManage, Title: "Management Commands:"},
	)
	for _, c := range root.Commands() {
		switch c.Name() {
		case "query", "txn":
			c.GroupID = groupSQL
		case "schema", "sample", "tables", "databases", "read", "explore", "analyze":
			c.GroupID = groupSchema
		case "config", "skill", "init", "version", "agent":
			c.GroupID = groupManage
		}
	}
	// Append agent-facing notes to the default help template. SetHelpTemplate
	// only affects the root command, so `mysql-cli <sub> --help` stays concise.
	root.SetHelpTemplate(root.HelpTemplate() + agentNotesTemplate)
}

// agentNotesTemplate is appended to the root help output. It surfaces the
// agent-facing contract (output format, exit codes, safety tiers, skill
// commands) that an AI caller needs to use mysql-cli correctly.
const agentNotesTemplate = `
Agent notes:
  Output: JSON by default (agent-friendly); switch with -f table|csv|tsv|jsonl.
  Exit codes: 0 OK | 2 conn | 3 readonly | 4 ddl-needs-write | 5 destructive-needs-yes
    | 6 identifier | 7 multi-statement | 8 sql | 9 timeout | 10 config | 11 init.
  Safety: read-only by default. DML needs --write; DDL needs --write --ddl;
    DROP/TRUNCATE and WHERE-less UPDATE/DELETE need --yes.
  Skills: 'mysql-cli skill install' installs agent skills;
    'mysql-cli skill check' verifies version sync.
  Write guard: 'mysql-cli agent init' installs per-agent configs that prompt a
    human before mysql-cli writes (--write/--ddl/--yes).
`
