// Package query executes SQL through a conn.Pool, enforcing the safety
// gate, single-statement rule, optional row limit, and context timeout.
package query

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/AllenMuu/mysql-cli/internal/safety"
)

// Options controls execution permissions and limits.
type Options struct {
	Write   bool
	DDL     bool
	Yes     bool
	Limit   int
	Timeout time.Duration
}

// Typed errors mapped by the cli layer to exit codes.
var (
	ErrGuard          = errors.New("guard")
	ErrMultiStatement = errors.New("multi-statement")
	ErrSQL            = errors.New("sql")
	ErrTimeout        = errors.New("timeout")
)

var selectRe = regexp.MustCompile(`(?i)^\s*(SELECT|WITH)\b`)

// Execute runs a single SQL statement. It first applies the safety guard,
// then the multi-statement check, then executes with the given timeout and
// optional LIMIT wrapping for SELECT queries.
func Execute(ctx context.Context, pool *conn.Pool, sqlText string, opts Options) (result.Result, error) {
	if safety.HasMultiStatement(sqlText) {
		return result.Empty(), ErrMultiStatement
	}
	if _, err := safety.Check(sqlText, safety.CheckOptions{Yes: opts.Yes}); err != nil {
		return result.Empty(), fmt.Errorf("%w: %v", ErrGuard, err)
	}

	execSQL := applyLimit(sqlText, opts.Limit)

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	rows, err := pool.DB.QueryContext(ctx, execSQL)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return result.Empty(), fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
	}
	res := result.Result{Columns: cols}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
		}
		res.Rows = append(res.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return result.Empty(), fmt.Errorf("%w: %v", ErrSQL, err)
	}
	return res, nil
}

// applyLimit wraps a SELECT with an outer LIMIT when one is requested and
// the statement is a read query without its own LIMIT.
func applyLimit(sqlText string, limit int) string {
	if limit <= 0 || !selectRe.MatchString(sqlText) {
		return sqlText
	}
	if hasLimit(sqlText) {
		return sqlText
	}
	// Strip a trailing semicolon (and surrounding whitespace) so the wrapped
	// subquery remains valid SQL.
	cleaned := strings.TrimRight(strings.TrimSpace(sqlText), ";")
	return fmt.Sprintf("SELECT * FROM (%s) AS _q LIMIT %d", cleaned, limit)
}

var ownLimitRe = regexp.MustCompile(`(?i)\bLIMIT\b\s+\d+`)

// hasLimit reports whether sqlText appears to already contain a LIMIT clause.
// It is a simple regex heuristic: LIMIT inside a string literal will be
// false-matched. Callers that need perfect SQL parsing should not rely on this.
func hasLimit(sqlText string) bool {
	return ownLimitRe.MatchString(sqlText)
}
