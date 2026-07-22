package schema

import (
	"context"
	"errors"
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

func TestExploreEmptyDatabases(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(sqlmock.NewRows([]string{"Database"}))
	r, err := Explore(context.Background(), pool)
	assert.NoError(t, err)
	assert.Equal(t, []string{"database", "table"}, r.Columns)
	assert.Empty(t, r.Rows)
}

func TestExploreTablesError(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(sqlmock.NewRows([]string{"Database"}).AddRow("app"))
	tablesErr := errors.New("tables exploded")
	mock.ExpectQuery("SHOW TABLES FROM `app`").WillReturnError(tablesErr)
	_, err := Explore(context.Background(), pool)
	assert.ErrorIs(t, err, tablesErr)
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

func TestAnalyzeTruncatesWideSample(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM information_schema.COLUMNS").WillReturnRows(
		sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT"}).AddRow("id", "int", "NO", nil, ""))
	mock.ExpectQuery("FROM `users` LIMIT 5").WillReturnRows(
		sqlmock.NewRows([]string{"a", "b", "c", "d", "e", "f", "g"}).AddRow(1, 2, 3, 4, 5, 6, 7))
	r, err := Analyze(context.Background(), pool, "users")
	assert.NoError(t, err)
	assert.Equal(t, 2, len(r.Rows))
	for _, row := range r.Rows {
		assert.Equal(t, 6, len(row))
	}
	assert.Equal(t, "sample", r.Rows[1][0])
	assert.Equal(t, "5", r.Rows[1][5])
}

func TestAnalyzeSampleMoreThanFiveRows(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM information_schema.COLUMNS").WillReturnRows(
		sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT"}).AddRow("id", "int", "NO", nil, ""))
	mock.ExpectQuery("FROM `users` LIMIT 5").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3).AddRow(4).AddRow(5).AddRow(6))
	r, err := Analyze(context.Background(), pool, "users")
	assert.NoError(t, err)
	sampleCount := 0
	for _, row := range r.Rows {
		if row[0] == "sample" {
			sampleCount++
		}
	}
	assert.LessOrEqual(t, sampleCount, 5)
}
