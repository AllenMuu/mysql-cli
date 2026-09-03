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

// TestExploreEmptyRowsNoPanic 验证驱动异常返回 0 列行时不 panic（issue #18）。
func TestExploreEmptyRowsNoPanic(t *testing.T) {
	t.Run("empty_database_row", func(t *testing.T) {
		pool, mock := newMock(t)
		// 0 列的数据库行：之前会因 drow[0] 越界 panic。
		mock.ExpectQuery("SHOW DATABASES").WillReturnRows(sqlmock.NewRows([]string{}).AddRow())
		r, err := Explore(context.Background(), pool)
		assert.NoError(t, err)
		assert.Empty(t, r.Rows) // 空行被跳过
	})
	t.Run("empty_table_row", func(t *testing.T) {
		pool, mock := newMock(t)
		mock.ExpectQuery("SHOW DATABASES").WillReturnRows(sqlmock.NewRows([]string{"Database"}).AddRow("app"))
		// 0 列的表行：之前会因 trow[0] 越界 panic。
		mock.ExpectQuery("SHOW TABLES FROM `app`").WillReturnRows(sqlmock.NewRows([]string{}).AddRow())
		r, err := Explore(context.Background(), pool)
		assert.NoError(t, err)
		assert.Empty(t, r.Rows) // 空表行被跳过
	})
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
	assert.Equal(t, "id", r.Rows[0][1])    // schema row: section,col,...
	assert.EqualValues(t, 1, r.Rows[1][1]) // sample row (int preserved)
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
	assert.EqualValues(t, 5, r.Rows[1][5])
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

// TestPadRowFillsNilForMissingColumns 验证列数不足时 padRow 用 nil 填充，
// 而非 ""（保留 NULL 语义，让 format 层正确渲染 NULL/null）。
func TestPadRowFillsNilForMissingColumns(t *testing.T) {
	// 输入只有 2 列，宽度要求 5 -> 后 3 列应为 nil。
	row := []any{"id", 42}
	out := padRow("schema", row, 5)
	assert.Equal(t, 6, len(out))
	assert.Equal(t, "schema", out[0])
	assert.Equal(t, "id", out[1])
	assert.EqualValues(t, 42, out[2])
	// 第 4~6 位（i=3,4,5）应为 nil 而非空字符串。
	assert.Nil(t, out[3])
	assert.Nil(t, out[4])
	assert.Nil(t, out[5])
}

// TestAnalyzeNarrowSamplePadsWithNil 通过 Analyze 端到端验证：sample 行列数不足时，
// 输出的尾部单元格是 nil（保留 NULL 语义，issue #22）。
func TestAnalyzeNarrowSamplePadsWithNil(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM information_schema.COLUMNS").WillReturnRows(
		sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT"}).AddRow("id", "int", "NO", nil, ""))
	// sample 只有 1 列，但 Analyze 输出宽度为 5 -> 后 4 列应为 nil。
	mock.ExpectQuery("FROM `users` LIMIT 5").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow(1))
	r, err := Analyze(context.Background(), pool, "users")
	assert.NoError(t, err)
	// 找到 sample 行，验证尾部单元格是 nil 而非 ""。
	var sampleRow []any
	for _, row := range r.Rows {
		if row[0] == "sample" {
			sampleRow = row
			break
		}
	}
	assert.NotNil(t, sampleRow)
	assert.EqualValues(t, 1, sampleRow[1])
	for i := 2; i <= 5; i++ {
		assert.Nil(t, sampleRow[i], "sample 行第 %d 列应为 nil 而非空字符串", i)
	}
}
