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

// TestExecuteTxnRejectsDDL 验证 A8：MySQL 的 DDL 会隐式提交当前事务，破坏
// txn 的原子性承诺，ExecuteTxn 必须拒绝 DDL（即使 --write/--ddl/--yes 齐全），
// 且在触碰数据库之前就返回。
func TestExecuteTxnRejectsDDL(t *testing.T) {
	pool, mock := newMock(t)
	_, err := ExecuteTxn(context.Background(), pool,
		[]string{"INSERT INTO t VALUES (1)", "CREATE TABLE t2 (id INT)", "INSERT INTO t VALUES (2)"},
		Options{Write: true, DDL: true, Yes: true})
	assert.ErrorIs(t, err, ErrGuard)
	assert.Contains(t, err.Error(), "implicit")
	assert.NotErrorIs(t, err, ErrSQL)
	// 连接前即拒绝：不应有任何 Begin/Exec/Commit 发生。
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecuteWriteSingleDDLStillAllowed 对照：单条 DDL 走 ExecuteWrite 保持允许
// （事务外没有原子性问题，DDL 自身就是即时提交的）。
func TestExecuteWriteSingleDDLStillAllowed(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("CREATE TABLE t2").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	_, err := ExecuteWrite(context.Background(), pool, "CREATE TABLE t2 (id INT)", Options{Write: true, DDL: true})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
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
