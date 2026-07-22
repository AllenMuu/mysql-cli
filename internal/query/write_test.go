package query

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestExecuteWriteDML(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE t SET a=1 WHERE id=1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	r, err := ExecuteWrite(context.Background(), pool, "UPDATE t SET a=1 WHERE id=1", Options{Write: true})
	assert.NoError(t, err)
	assert.Equal(t, int64(1), r.RowsAffected)
}

func TestExecuteWriteGuardFails(t *testing.T) {
	pool, _ := newMock(t)
	_, err := ExecuteWrite(context.Background(), pool, "UPDATE t SET a=1", Options{})
	assert.ErrorIs(t, err, ErrGuard)
}

func TestExecuteTxnAtomic(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO t VALUES \\(1\\)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE t SET a=2").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	r, err := ExecuteTxn(context.Background(), pool, []string{"INSERT INTO t VALUES (1)", "UPDATE t SET a=2"}, Options{Write: true, Yes: true})
	assert.NoError(t, err)
	assert.Equal(t, int64(2), r.RowsAffected)
}

func TestExecuteTxnRollbackOnError(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO t VALUES \\(1\\)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("BAD").WillReturnError(assert.AnError)
	mock.ExpectRollback()
	_, err := ExecuteTxn(context.Background(), pool, []string{"INSERT INTO t VALUES (1)", "BAD"}, Options{Write: true})
	assert.Error(t, err)
}

func TestExecuteTxnRequiresWrite(t *testing.T) {
	pool, _ := newMock(t)
	_, err := ExecuteTxn(context.Background(), pool, []string{"INSERT INTO t VALUES (1)"}, Options{})
	assert.ErrorIs(t, err, ErrGuard)
}
