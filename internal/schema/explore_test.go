package schema

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestExploreCombinesDbsAndTables(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(sqlmock.NewRows([]string{"Database"}).AddRow("app"))
	mock.ExpectQuery("SHOW TABLES FROM `app`").WillReturnRows(sqlmock.NewRows([]string{"Tables_in_app"}).AddRow("users").AddRow("orders"))
	r, err := Explore(context.Background(), pool)
	assert.NoError(t, err)
	assert.Equal(t, []string{"database", "table"}, r.Columns)
	assert.Equal(t, 2, len(r.Rows))
	assert.Equal(t, "app", r.Rows[0][0])
}

func TestAnalyzeCombinesSchemaAndSample(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM information_schema.COLUMNS").WillReturnRows(
		sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT"}).AddRow("id", "int", "NO", nil, ""))
	mock.ExpectQuery("FROM `users` LIMIT 5").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow(1))
	r, err := Analyze(context.Background(), pool, "users")
	assert.NoError(t, err)
	// Two sections: schema columns then sample columns.
	assert.Equal(t, 2, len(r.Rows))
	assert.Equal(t, "id", r.Rows[0][1]) // schema row: section,col,...
	assert.Equal(t, "1", r.Rows[1][1])  // sample row
}