package cli

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/stretchr/testify/assert"
)

func TestOpts(t *testing.T) {
	g := &Globals{
		Write:   true,
		DDL:     true,
		Yes:     true,
		Limit:   42,
		Timeout: "5s",
	}
	opts := g.opts()
	assert.True(t, opts.Write)
	assert.True(t, opts.DDL)
	assert.True(t, opts.Yes)
	assert.Equal(t, 42, opts.Limit)
	assert.Equal(t, 5*time.Second, opts.Timeout)
}

func TestEmitResultSuccess(t *testing.T) {
	var buf bytes.Buffer
	g := &Globals{out: &buf, Format: "json"}
	g.emitResult(result.Result{Columns: []string{"x"}, Rows: [][]any{{1}}}, nil)
	assert.Contains(t, buf.String(), `"columns":["x"]`)
}

func TestEmitResultError(t *testing.T) {
	var buf bytes.Buffer
	g := &Globals{out: &buf, Format: "table"}
	g.emitResult(result.Empty(), errors.New("boom"))
	assert.Contains(t, buf.String(), "Error [CONFIG_ERROR]: boom")
}
