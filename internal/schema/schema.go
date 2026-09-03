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
	return result.ScanRows(rows)
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
//
// db 是用户输入（CLI 参数），必须经 safety.ValidateIdentifier 白名单校验防注入。
// 对于来自 MySQL 自身返回的库名（如 Explore 从 SHOW DATABASES 拿到），调用方
// 应改用 tablesInDB（反引号包裹即足够安全）——因为 MySQL 允许 `my-app`、`my.db`
// 等含特殊字符的库名，白名单会拒绝它们导致 Explore 失败。
func Tables(ctx context.Context, pool *conn.Pool, db string) (result.Result, error) {
	if db != "" {
		if err := safety.ValidateIdentifier(db); err != nil {
			return result.Empty(), err
		}
		return tablesInDB(ctx, pool, db)
	}
	return queryRows(ctx, pool, "SHOW TABLES")
}

// tablesInDB 用反引号包裹 db 后执行 SHOW TABLES FROM `<db>`。
// 调用方必须保证 db 来自可信来源（如 MySQL 自身返回的库名），不可用于用户输入。
// 反引号包裹已足够防 SQL 注入；db 中的反引号字符本身（罕见）会被 MySQL 视为
// 标识符边界，但仍不会导致注入（标识符上下文里没有可执行的语句）。
func tablesInDB(ctx context.Context, pool *conn.Pool, db string) (result.Result, error) {
	return queryRows(ctx, pool, fmt.Sprintf("SHOW TABLES FROM `%s`", db))
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
