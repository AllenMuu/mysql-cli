package schema

import (
	"context"

	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/result"
)

// Explore returns one row per (database, table) across non-system databases.
func Explore(ctx context.Context, pool *conn.Pool) (result.Result, error) {
	dbs, err := Databases(ctx, pool)
	if err != nil {
		return result.Empty(), err
	}
	out := result.Result{Columns: []string{"database", "table"}}
	for _, drow := range dbs.Rows {
		// 驱动异常时可能返回空行，先校验长度避免 drow[0] 越界 panic。
		if len(drow) == 0 {
			continue
		}
		dbName, ok := drow[0].(string)
		if !ok {
			continue
		}
		// dbName 来自 SHOW DATABASES（MySQL 自身返回），可信；用 tablesInDB
		// 跳过白名单校验，因为 MySQL 允许 my-app / my.db 等库名，白名单会拒绝。
		tbls, err := tablesInDB(ctx, pool, dbName)
		if err != nil {
			return result.Empty(), err
		}
		for _, trow := range tbls.Rows {
			// 同上，trow 可能是空切片，越界访问会 panic。
			if len(trow) == 0 {
				continue
			}
			tName, _ := trow[0].(string)
			out.Rows = append(out.Rows, []any{dbName, tName})
		}
	}
	return out, nil
}

// Analyze returns a combined view: schema columns followed by a 5-row sample.
// Rows are tagged by section in the first column so an agent can distinguish.
func Analyze(ctx context.Context, pool *conn.Pool, table string) (result.Result, error) {
	sc, err := Schema(ctx, pool, table)
	if err != nil {
		return result.Empty(), err
	}
	sm, err := Sample(ctx, pool, table, 5)
	if err != nil {
		return result.Empty(), err
	}
	const sampleWidth = 5
	out := result.Result{Columns: []string{"section", "col1", "col2", "col3", "col4", "col5"}}
	for _, row := range sc.Rows {
		out.Rows = append(out.Rows, padRow("schema", row, sampleWidth))
	}
	for i, row := range sm.Rows {
		if i >= sampleWidth {
			break
		}
		out.Rows = append(out.Rows, padRow("sample", row, sampleWidth))
	}
	return out, nil
}

func padRow(section string, row []any, width int) []any {
	out := make([]any, width+1)
	out[0] = section
	for i := 0; i < width; i++ {
		if i < len(row) {
			// Preserve the original value (nil/int/string) so the format
			// layer renders NULL (table) / null (JSON) and numbers correctly,
			// instead of stringifying via fmt.Sprintf.
			out[i+1] = row[i]
		} else {
			// 列数不足时用 nil 填充，保留 NULL 语义；用 "" 会让 format 层渲染成空字符串。
			out[i+1] = nil
		}
	}
	return out
}