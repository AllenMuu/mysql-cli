package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// captureRootHelp executes the root command with the given args and returns
// its captured stdout. Used to assert on `--help` / `help` output without
// touching the real process stdout.
func captureRootHelp(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	g := &Globals{Format: "json", out: &out, eout: &out}
	root := newRootCmd(g)
	root.SetOut(&out)
	root.SetArgs(args)
	err := root.Execute()
	assert.NoError(t, err)
	return out.String()
}

func TestRootHelpListsCommandGroups(t *testing.T) {
	s := captureRootHelp(t, "--help")
	for _, want := range []string{
		"SQL Commands:",
		"Schema Exploration Commands:",
		"Management Commands:",
	} {
		assert.Contains(t, s, want)
	}
}

func TestRootHelpShowsSubcommandShorts(t *testing.T) {
	s := captureRootHelp(t, "--help")
	for _, want := range []string{
		"Run a SQL statement (read by default; --write for DML)",
		"Run multiple statements in one atomic transaction",
		"Show table structure, or whole database if no table given",
		"Sample rows from a table (-n, max 20)",
		"List tables in a database",
		"List databases",
		"First 100 rows of a table",
		"Database and table overview",
		"Schema + sample in one shot",
	} {
		assert.Contains(t, s, want)
	}
}

func TestRootHelpIncludesAgentNotes(t *testing.T) {
	s := captureRootHelp(t, "--help")
	for _, want := range []string{
		"Agent notes",
		"Exit codes",
		"npx skills add",
		"read-only by default",
	} {
		assert.Contains(t, s, want)
	}
}

// `mysql-cli help` (the subcommand form) must render the same grouped help as
// `mysql-cli --help`, including the Agent notes section.
func TestHelpSubcommandMatchesFlagForm(t *testing.T) {
	s := captureRootHelp(t, "help")
	assert.Contains(t, s, "Agent notes")
	assert.Contains(t, s, "SQL Commands:")
	assert.Contains(t, s, "Schema + sample in one shot")
}
