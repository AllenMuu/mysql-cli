package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionFlag(t *testing.T) {
	var buf bytes.Buffer
	g := &Globals{Format: "json", out: &buf}
	root := newRootCmd(g)
	root.SetArgs([]string{"--version"})
	require.NoError(t, root.Execute())
	assert.Contains(t, buf.String(), "mysql-cli version")
	// version defaults to "dev" when built without ldflags (e.g. go test).
	assert.Contains(t, buf.String(), "dev")
}

func TestVersionSubcommand(t *testing.T) {
	var buf bytes.Buffer
	g := &Globals{Format: "json", out: &buf}
	root := newRootCmd(g)
	root.SetArgs([]string{"version"})
	require.NoError(t, root.Execute())
	assert.Contains(t, buf.String(), "mysql-cli version")
	assert.Contains(t, buf.String(), "dev")
}
