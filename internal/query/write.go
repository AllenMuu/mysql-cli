package query

import (
	"context"
	"fmt"

	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/AllenMuu/mysql-cli/internal/safety"
)

// ExecuteWrite runs a single DML/DDL statement inside a transaction and
// commits. It enforces the same guard as Execute.
func ExecuteWrite(ctx context.Context, pool *conn.Pool, sqlText string, opts Options) (result.Result, error) {
	if safety.HasMultiStatement(sqlText) {
		return result.Empty(), ErrMultiStatement
	}
	if _, err := safety.Check(sqlText, safety.CheckOptions{Write: opts.Write, DDL: opts.DDL, Yes: opts.Yes}); err != nil {
		return result.Empty(), fmt.Errorf("%w: %w", ErrGuard, err)
	}

	// 写路径同样应用 --timeout，避免卡死的 UPDATE/DDL 让进程挂起。
	// opts.Timeout<=0 时跳过，保持原有行为。
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		return result.Empty(), fmt.Errorf("%w: %w", ErrSQL, err)
	}
	res, err := tx.ExecContext(ctx, sqlText)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return result.Empty(), fmt.Errorf("%w: %w; rollback failed: %v", ErrSQL, err, rbErr)
		}
		return result.Empty(), fmt.Errorf("%w: %w", ErrSQL, err)
	}
	if err := tx.Commit(); err != nil {
		return result.Empty(), fmt.Errorf("%w: %w", ErrSQL, err)
	}
	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	return result.Result{RowsAffected: affected, LastInsertID: lastID}, nil
}

// ExecuteTxn runs multiple statements in one transaction atomically. Every
// statement is individually guarded; any error rolls back.
func ExecuteTxn(ctx context.Context, pool *conn.Pool, statements []string, opts Options) (result.Result, error) {
	if !opts.Write {
		return result.Empty(), fmt.Errorf("%w: txn requires --write", ErrGuard)
	}
	if len(statements) == 0 {
		return result.Empty(), nil
	}
	for _, s := range statements {
		if safety.HasMultiStatement(s) {
			return result.Empty(), ErrMultiStatement
		}
		if _, err := safety.Check(s, safety.CheckOptions{Write: opts.Write, DDL: opts.DDL, Yes: opts.Yes}); err != nil {
			return result.Empty(), fmt.Errorf("%w: %w", ErrGuard, err)
		}
	}

	// 写路径同样应用 --timeout，覆盖整个事务执行阶段。
	// opts.Timeout<=0 时跳过，保持原有行为。
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		return result.Empty(), fmt.Errorf("%w: %w", ErrSQL, err)
	}
	var total int64
	for _, s := range statements {
		res, err := tx.ExecContext(ctx, s)
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				return result.Empty(), fmt.Errorf("%w: %w; rollback failed: %v", ErrSQL, err, rbErr)
			}
			return result.Empty(), fmt.Errorf("%w: %w", ErrSQL, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if err := tx.Commit(); err != nil {
		return result.Empty(), fmt.Errorf("%w: %w", ErrSQL, err)
	}
	return result.Result{RowsAffected: total}, nil
}
