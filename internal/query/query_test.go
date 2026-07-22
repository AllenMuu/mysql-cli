package query

import (
	"context"
	"database/sql"
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
	assert.Error(t, err)
}

func TestExecuteSQLDriverError(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("SELECT bad").WillReturnError(sql.ErrNoRows)
	_, err := Execute(context.Background(), pool, "SELECT bad", Options{})
	assert.Error(t, err)
}
