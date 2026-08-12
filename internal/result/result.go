// Package result defines the shared query result type exchanged between
// the query/schema packages (producers) and the format/cli packages
// (consumers). It is a dependency-free base layer to avoid import cycles.
package result

import (
	"database/sql"
)

// Result is the uniform outcome of any database operation.
// For SELECT-like queries Columns and Rows are populated.
// For DML/DDL RowsAffected (and LastInsertID where available) are populated.
type Result struct {
	Columns      []string
	Rows         [][]any
	RowsAffected int64
	LastInsertID int64
	Truncated    bool
}

// Empty returns a zero-valued Result for operations that produce no rows.
func Empty() Result {
	return Result{}
}

// ScanRows 把 *sql.Rows 扫描成 Result{Columns, Rows}。
// RowsAffected/LastInsertID 不在此设置（写路径用不到本函数），由调用方按需填充。
//
// 行为约定（与原 query/schema 包内联扫描循环逐字一致）：
//   - 驱动对文本列返回 []byte，这里转成 string，否则 JSON 输出会变成 base64。
//   - 扫描结束后检查 rows.Err()，捕捉迭代期间的延迟错误。
//
// 调用方负责关闭 rows（defer rows.Close()）。
func ScanRows(rows *sql.Rows) (Result, error) {
	cols, err := rows.Columns()
	if err != nil {
		return Empty(), err
	}
	res := Result{Columns: cols}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Empty(), err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		res.Rows = append(res.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return Empty(), err
	}
	return res, nil
}
