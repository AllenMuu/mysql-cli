package schema

import (
	"context"
	"fmt"

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
		dbName, _ := drow[0].(string)
		tbls, err := Tables(ctx, pool, dbName)
		if err != nil {
			continue
		}
		for _, trow := range tbls.Rows {
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
	out := result.Result{Columns: []string{"section", "col1", "col2", "col3", "col4", "col5"}}
	for _, row := range sc.Rows {
		out.Rows = append(out.Rows, padRow("schema", row, 5))
	}
	for _, row := range sm.Rows {
		out.Rows = append(out.Rows, padRow("sample", row, 5))
	}
	return out, nil
}

func padRow(section string, row []any, width int) []any {
	out := make([]any, 0, width+1)
	out = append(out, section)
	for i := 0; i < width; i++ {
		if i < len(row) {
			out = append(out, fmt.Sprintf("%v", row[i]))
		} else {
			out = append(out, "")
		}
	}
	return out
}