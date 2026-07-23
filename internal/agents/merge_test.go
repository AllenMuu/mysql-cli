package agents

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillBody(t *testing.T) {
	in := "---\nname: x\nversion: 1.0.0\n---\n\nbody line 1\nbody line 2\n"
	assert.Equal(t, "\nbody line 1\nbody line 2\n", SkillBody(in))
}

func TestSkillBody_NoFrontmatter(t *testing.T) {
	assert.Equal(t, "", SkillBody("no delimiters here"))
}

func TestMergedBody(t *testing.T) {
	got, err := mergedBody(testFS(), testNames)
	require.NoError(t, err)
	// sorted order: mysql-query, mysql-schema, mysql-shared
	assert.Contains(t, got, "## mysql-cli skill: mysql-query")
	assert.Contains(t, got, "## mysql-cli skill: mysql-schema")
	assert.Contains(t, got, "## mysql-cli skill: mysql-shared")
	assert.Contains(t, got, "query body")
	assert.Less(t, strings.Index(got, "mysql-query"), strings.Index(got, "mysql-schema"))
}

func TestMergeInstructionFile_AppendWhenAbsent(t *testing.T) {
	got := MergeInstructionFile("", "MERGED")
	assert.Contains(t, got, beginMarker)
	assert.Contains(t, got, endMarker)
	assert.Contains(t, got, "MERGED")
	assert.Contains(t, got, updateNote)
}

func TestMergeInstructionFile_ReplacesExistingBlock(t *testing.T) {
	existing := "user notes\n\n" + beginMarker + "\nold\n" + endMarker + "\n"
	got := MergeInstructionFile(existing, "NEW")
	assert.Contains(t, got, "user notes")
	assert.Contains(t, got, "NEW")
	assert.NotContains(t, got, "old")
}

func TestMergeInstructionFile_Idempotent(t *testing.T) {
	merged, _ := mergedBody(testFS(), testNames)
	once := MergeInstructionFile("", merged)
	twice := MergeInstructionFile(once, merged)
	assert.Equal(t, once, twice, "re-running must not accumulate whitespace or duplicates")
}

func TestMakeMDC(t *testing.T) {
	got := makeMDC("mysql-query", "BODY")
	assert.Equal(t, "---\ndescription: Run SQL with mysql-cli: SELECT query, txn, DML (INSERT/UPDATE/DELETE), DDL\nglobs: *.sql\nalwaysApply: false\n---\nBODY", got)
}