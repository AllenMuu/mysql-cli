package query

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func newMock(t *testing.T) (*conn.Pool, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	return &conn.Pool{DB: db}, mock
}

func TestExecuteReadReturnsRows(t *testing.T) {
	pool, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "a").AddRow(nil, "b")
	mock.ExpectQuery("SELECT id, name FROM users").WillReturnRows(rows)
	r, err := Execute(context.Background(), pool, "SELECT id, name FROM users", Options{})
	assert.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, r.Columns)
	assert.Equal(t, 2, len(r.Rows))
	assert.Equal(t, nil, r.Rows[1][0])
}

func TestExecuteReadonlyViolation(t *testing.T) {
	pool, _ := newMock(t)
	_, err := Execute(context.Background(), pool, "UPDATE users SET a=1 WHERE id=1", Options{})
	assert.ErrorIs(t, err, ErrGuard)
}

func TestExecuteMultiStatementRejected(t *testing.T) {
	pool, _ := newMock(t)
	_, err := Execute(context.Background(), pool, "SELECT 1; SELECT 2", Options{})
	assert.ErrorIs(t, err, ErrMultiStatement)
}

func TestExecuteLimitWrapsQuery(t *testing.T) {
	pool, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id"}).AddRow(1)
	mock.ExpectQuery("SELECT \\* FROM \\(SELECT id FROM t\\) AS _q LIMIT 100").WillReturnRows(rows)
	r, err := Execute(context.Background(), pool, "SELECT id FROM t", Options{Limit: 100})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(r.Rows))
}

func TestExecuteTimeout(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("SELECT 1").WillDelayFor(200 * time.Millisecond).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	_, err := Execute(context.Background(), pool, "SELECT 1", Options{Timeout: 50 * time.Millisecond})
	assert.ErrorIs(t, err, ErrTimeout)
}

func TestExecuteSQLDriverError(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("SELECT bad").WillReturnError(sql.ErrNoRows)
	_, err := Execute(context.Background(), pool, "SELECT bad", Options{})
	assert.ErrorIs(t, err, ErrSQL)
}

func TestApplyLimit(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		limit    int
		expected string
	}{
		{
			name:     "no limit returns original",
			sql:      "SELECT id FROM t",
			limit:    0,
			expected: "SELECT id FROM t",
		},
		{
			name:     "non-SELECT returns original",
			sql:      "UPDATE t SET x=1",
			limit:    10,
			expected: "UPDATE t SET x=1",
		},
		{
			name:     "existing LIMIT returns original",
			sql:      "SELECT id FROM t LIMIT 5",
			limit:    10,
			expected: "SELECT id FROM t LIMIT 5",
		},
		{
			name:     "wraps SELECT with subquery",
			sql:      "SELECT id FROM t",
			limit:    100,
			expected: "SELECT * FROM (SELECT id FROM t) AS _q LIMIT 100",
		},
		{
			name:     "strips trailing semicolon before wrapping",
			sql:      "SELECT id FROM t;",
			limit:    100,
			expected: "SELECT * FROM (SELECT id FROM t) AS _q LIMIT 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, applyLimit(tt.sql, tt.limit, false))
		})
	}
}

func TestApplyLimitIgnoresLimitInStringLiteral(t *testing.T) {
	// Documenting current regex behavior: LIMIT inside a string literal is
	// treated as a real LIMIT clause, so the query is not wrapped. This is a
	// known limitation of the simple heuristic used by hasLimit.
	sql := "SELECT 'LIMIT 10' FROM t"
	assert.Equal(t, sql, applyLimit(sql, 100, false))
}

func TestApplyLimitProbe(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		limit    int
		probe    bool
		expected string
	}{
		{name: "probe wraps limit+1", sql: "SELECT id FROM t", limit: 100, probe: true, expected: "SELECT * FROM (SELECT id FROM t) AS _q LIMIT 101"},
		{name: "no probe wraps limit", sql: "SELECT id FROM t", limit: 100, probe: false, expected: "SELECT * FROM (SELECT id FROM t) AS _q LIMIT 100"},
		{name: "probe ignored when limit<=0", sql: "SELECT id FROM t", limit: 0, probe: true, expected: "SELECT id FROM t"},
		{name: "probe ignored when hasLimitClause", sql: "SELECT id FROM t LIMIT 5", limit: 100, probe: true, expected: "SELECT id FROM t LIMIT 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, applyLimit(tt.sql, tt.limit, tt.probe))
		})
	}
}

func TestExecuteProbeTruncates(t *testing.T) {
	pool, mock := newMock(t)
	// probe limit=2 -> wraps LIMIT 3, 返回 3 行 -> truncated, 保留 2 行
	rows := sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3)
	mock.ExpectQuery("SELECT \\* FROM \\(SELECT id FROM t\\) AS _q LIMIT 3").WillReturnRows(rows)
	r, err := Execute(context.Background(), pool, "SELECT id FROM t", Options{Limit: 2, Probe: true})
	assert.NoError(t, err)
	assert.True(t, r.Truncated)
	assert.Equal(t, 2, len(r.Rows))
}

func TestExecuteProbeNoTruncate(t *testing.T) {
	pool, mock := newMock(t)
	// probe limit=2 -> wraps LIMIT 3, 返回 2 行(<=limit) -> 未截断
	rows := sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2)
	mock.ExpectQuery("SELECT \\* FROM \\(SELECT id FROM t\\) AS _q LIMIT 3").WillReturnRows(rows)
	r, err := Execute(context.Background(), pool, "SELECT id FROM t", Options{Limit: 2, Probe: true})
	assert.NoError(t, err)
	assert.False(t, r.Truncated)
	assert.Equal(t, 2, len(r.Rows))
}

func TestExecuteProbeSkipsNonSelect(t *testing.T) {
	// SHOW/DESCRIBE/EXPLAIN are CategoryRead but applyLimit refuses to wrap
	// them (selectRe only matches SELECT|WITH). The post-scan truncation
	// must also skip them so a large SHOW result is not silently truncated.
	pool, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"Variable_name", "Value"}).
		AddRow("a", fmt.Sprintf("v%d", 1)).
		AddRow("b", fmt.Sprintf("v%d", 2)).
		AddRow("c", fmt.Sprintf("v%d", 3))
	// ExpectQuery takes a regex; escape the space and treat the query literally.
	mock.ExpectQuery("SHOW VARIABLES").WillReturnRows(rows)
	r, err := Execute(context.Background(), pool, "SHOW VARIABLES", Options{Limit: 2, Probe: true})
	assert.NoError(t, err)
	assert.False(t, r.Truncated)
	assert.Equal(t, 3, len(r.Rows))
}
