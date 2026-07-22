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
		return result.Empty(), fmt.Errorf("%w: %v", ErrGuard, err)
	}

	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
	}
	res, err := tx.ExecContext(ctx, sqlText)
	if err != nil {
		tx.Rollback()
		return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
	}
	if err := tx.Commit(); err != nil {
		return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
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
	for _, s := range statements {
		if safety.HasMultiStatement(s) {
			return result.Empty(), ErrMultiStatement
		}
		if _, err := safety.Check(s, safety.CheckOptions{Write: opts.Write, DDL: opts.DDL, Yes: opts.Yes}); err != nil {
			return result.Empty(), fmt.Errorf("%w: %v", ErrGuard, err)
		}
	}

	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
	}
	var total int64
	for _, s := range statements {
		res, err := tx.ExecContext(ctx, s)
		if err != nil {
			tx.Rollback()
			return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if err := tx.Commit(); err != nil {
		return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
	}
	return result.Result{RowsAffected: total}, nil
}
