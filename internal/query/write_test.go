package query

import (
	"context"
	"errors"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/result"
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

func TestExecuteTxnEmptyStatements(t *testing.T) {
	pool, _ := newMock(t)
	r, err := ExecuteTxn(context.Background(), pool, nil, Options{Write: true})
	assert.NoError(t, err)
	assert.Equal(t, result.Empty(), r)
}

func TestExecuteWriteLastInsertID(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO t VALUES \\(1\\)").WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectCommit()
	r, err := ExecuteWrite(context.Background(), pool, "INSERT INTO t VALUES (1)", Options{Write: true})
	assert.NoError(t, err)
	assert.Equal(t, int64(42), r.LastInsertID)
	assert.Equal(t, int64(1), r.RowsAffected)
}

func TestExecuteTxnRollbackOnError(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO t VALUES \\(1\\)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("BAD").WillReturnError(assert.AnError)
	mock.ExpectRollback()
	_, err := ExecuteTxn(context.Background(), pool, []string{"INSERT INTO t VALUES (1)", "BAD"}, Options{Write: true})
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSQL)
}

func TestExecuteTxnRequiresWrite(t *testing.T) {
	pool, _ := newMock(t)
	_, err := ExecuteTxn(context.Background(), pool, []string{"INSERT INTO t VALUES (1)"}, Options{})
	assert.ErrorIs(t, err, ErrGuard)
}

// TestExecuteTxnCommitFailure 验证事务全部 Exec 成功但 Commit 失败（如死锁检测）
// 时，error 被正确包装为 ErrSQL 哨兵且携带原始错误信息。
// write.go 的 Commit 失败路径用 fmt.Errorf("%w: %w", ErrSQL, err) 包装，
// 因此 errors.Is(err, ErrSQL) 成立。
func TestExecuteTxnCommitFailure(t *testing.T) {
	pool, mock := newMock(t)
	deadlockErr := errors.New("Error 1213: Deadlock found when trying to get lock; try restarting transaction")
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO t VALUES \\(1\\)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE t SET a=2").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(deadlockErr)
	_, err := ExecuteTxn(context.Background(), pool, []string{"INSERT INTO t VALUES (1)", "UPDATE t SET a=2"}, Options{Write: true, Yes: true})
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSQL)
	// 原始死锁 error 的信息应透传到 message 中。
	assert.Contains(t, err.Error(), "Deadlock")
}

// TestExecuteWriteCommitFailure 验证单语句写操作 Commit 失败时的 error 包装。
// 与 TestExecuteTxnCommitFailure 对应，覆盖 ExecuteWrite 的 Commit 失败分支。
func TestExecuteWriteCommitFailure(t *testing.T) {
	pool, mock := newMock(t)
	deadlockErr := errors.New("Error 1213: Deadlock found when trying to get lock; try restarting transaction")
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE t SET a=1 WHERE id=1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(deadlockErr)
	_, err := ExecuteWrite(context.Background(), pool, "UPDATE t SET a=1 WHERE id=1", Options{Write: true})
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSQL)
	assert.Contains(t, err.Error(), "Deadlock")
}
