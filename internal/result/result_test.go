package result

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmptyResult(t *testing.T) {
	r := Empty()
	assert.Empty(t, r.Columns)
	assert.Empty(t, r.Rows)
	assert.Equal(t, int64(0), r.RowsAffected)
}

func TestResultHoldsData(t *testing.T) {
	r := Result{
		Columns:      []string{"id", "name"},
		Rows:         [][]any{{1, "a"}, {nil, "b"}},
		RowsAffected: 2,
	}
	assert.Equal(t, []string{"id", "name"}, r.Columns)
	assert.Equal(t, nil, r.Rows[1][0])
}
