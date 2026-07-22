// Package schema implements the read-only exploration commands that mirror
// the original MCP's get_schema_info, get_table_sample, list_resources and
// read_resource. All identifiers are validated before interpolation.
package schema

import (
	"context"
	"fmt"

	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/AllenMuu/mysql-cli/internal/safety"
)

var systemDBs = map[string]bool{
	"information_schema": true,
	"mysql":              true,
	"performance_schema": true,
	"sys":                true,
}

func queryRows(ctx context.Context, pool *conn.Pool, sqlText string) (result.Result, error) {
	rows, err := pool.DB.QueryContext(ctx, sqlText)
	if err != nil {
		return result.Empty(), fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return result.Empty(), err
	}
	res := result.Result{Columns: cols}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return result.Empty(), err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		res.Rows = append(res.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return result.Empty(), err
	}
	return res, nil
}

// Databases lists non-system databases.
func Databases(ctx context.Context, pool *conn.Pool) (result.Result, error) {
	r, err := queryRows(ctx, pool, "SHOW DATABASES")
	if err != nil {
		return r, err
	}
	filtered := make([][]any, 0, len(r.Rows))
	for _, row := range r.Rows {
		if len(row) > 0 {
			if name, ok := row[0].(string); ok && systemDBs[name] {
				continue
			}
		}
		filtered = append(filtered, row)
	}
	return result.Result{Columns: r.Columns, Rows: filtered}, nil
}

// Tables lists tables in db (or the current database if db is empty).
func Tables(ctx context.Context, pool *conn.Pool, db string) (result.Result, error) {
	if db != "" {
		if err := safety.ValidateIdentifier(db); err != nil {
			return result.Empty(), err
		}
		return queryRows(ctx, pool, fmt.Sprintf("SHOW TABLES FROM `%s`", db))
	}
	return queryRows(ctx, pool, "SHOW TABLES")
}

// Schema returns column metadata for one table, or all tables when table is empty.
func Schema(ctx context.Context, pool *conn.Pool, table string) (result.Result, error) {
	if table == "" {
		q := "SELECT TABLE_NAME, COLUMN_NAME, DATA_TYPE, IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, ORDINAL_POSITION"
		return queryRows(ctx, pool, q)
	}
	db, tbl, err := safety.ValidateQualifiedTable(table)
	if err != nil {
		return result.Empty(), err
	}
	var q string
	if db != "" {
		q = fmt.Sprintf("SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_COMMENT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s' ORDER BY ORDINAL_POSITION", db, tbl)
	} else {
		q = fmt.Sprintf("SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_COMMENT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '%s' ORDER BY ORDINAL_POSITION", tbl)
	}
	return queryRows(ctx, pool, q)
}

// Sample returns up to limit rows from table; limit is clamped to [1,20].
func Sample(ctx context.Context, pool *conn.Pool, table string, limit int) (result.Result, error) {
	db, tbl, err := safety.ValidateQualifiedTable(table)
	if err != nil {
		return result.Empty(), err
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	var q string
	if db != "" {
		q = fmt.Sprintf("SELECT * FROM `%s`.`%s` LIMIT %d", db, tbl, limit)
	} else {
		q = fmt.Sprintf("SELECT * FROM `%s` LIMIT %d", tbl, limit)
	}
	return queryRows(ctx, pool, q)
}

// Read returns up to 100 rows from a table (mirrors read_resource).
func Read(ctx context.Context, pool *conn.Pool, table string) (result.Result, error) {
	db, tbl, err := safety.ValidateQualifiedTable(table)
	if err != nil {
		return result.Empty(), err
	}
	var q string
	if db != "" {
		q = fmt.Sprintf("SELECT * FROM `%s`.`%s` LIMIT 100", db, tbl)
	} else {
		q = fmt.Sprintf("SELECT * FROM `%s` LIMIT 100", tbl)
	}
	return queryRows(ctx, pool, q)
}