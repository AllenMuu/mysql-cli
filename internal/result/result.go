// Package result defines the shared query result type exchanged between
// the query/schema packages (producers) and the format/cli packages
// (consumers). It is a dependency-free base layer to avoid import cycles.
package result

// Result is the uniform outcome of any database operation.
// For SELECT-like queries Columns and Rows are populated.
// For DML/DDL RowsAffected (and LastInsertID where available) are populated.
type Result struct {
	Columns      []string
	Rows         [][]any
	RowsAffected int64
	LastInsertID int64
}

// Empty returns a zero-valued Result for operations that produce no rows.
func Empty() Result {
	return Result{}
}