package repl

import (
	"bytes"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/query"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatchQuit(t *testing.T) {
	code, _ := dispatch("\\q", Config{})
	assert.Equal(t, -1, code) // -1 signals exit
}

func TestDispatchUnknownSlash(t *testing.T) {
	_, msg := dispatch("\\bogus", Config{})
	assert.Contains(t, msg, "unknown")
}

func TestIsExit(t *testing.T) {
	assert.True(t, isExit(-1))
	assert.False(t, isExit(0))
}

func TestLooksLikeSQL(t *testing.T) {
	assert.True(t, looksLikeSQL("SELECT 1"))
	assert.False(t, looksLikeSQL("\\tables"))
}

func TestRunOnceQuit(t *testing.T) {
	assert.True(t, runOnce("\\q", Config{}))
}

func TestRunOnceUnknown(t *testing.T) {
	var buf bytes.Buffer
	assert.False(t, runOnce("hello world", Config{Out: &buf}))
	assert.Empty(t, buf.String())
}

func TestRunOnceSQLRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("SELECT 1").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	var buf bytes.Buffer
	pool := &conn.Pool{DB: db}
	cfg := Config{Pool: pool, Out: &buf, Format: "json"}
	assert.False(t, runOnce("SELECT 1", cfg))
	assert.Contains(t, buf.String(), `"success":true`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunOnceSQLWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE t SET a=1 WHERE id=1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	var buf bytes.Buffer
	pool := &conn.Pool{DB: db}
	cfg := Config{
		Pool:   pool,
		Out:    &buf,
		Opts:   query.Options{Write: true},
		Format: "json",
	}
	assert.False(t, runOnce("UPDATE t SET a=1 WHERE id=1", cfg))
	assert.Contains(t, buf.String(), `"rows_affected":1`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLooksLikeSQLNoFalsePositive(t *testing.T) {
	assert.False(t, looksLikeSQL("SELECTED FROM t"))
	assert.True(t, looksLikeSQL("SELECT 1"))
}
