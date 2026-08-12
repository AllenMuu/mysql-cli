package result

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestEmptyResult(t *testing.T) {
	r := Empty()
	assert.Empty(t, r.Columns)
	assert.Empty(t, r.Rows)
	assert.Equal(t, int64(0), r.RowsAffected)
}

func TestResultHoldsData(t *testing.T) {
	r := Result{
		Columns:      []string{"id", "name"},
		Rows:         [][]any{{1, "a"}, {nil, "b"}},
		RowsAffected: 2,
	}
	assert.Equal(t, []string{"id", "name"}, r.Columns)
	assert.Equal(t, nil, r.Rows[1][0])
}

func TestTruncatedField(t *testing.T) {
	r := Result{Columns: []string{"id"}, Rows: [][]any{{1}}, Truncated: true}
	assert.True(t, r.Truncated)

	zero := Result{}
	assert.False(t, zero.Truncated) // 零值为 false
}

// newMockRows 构造一个 sqlmock 并返回其 *sql.Rows（仅用于 ScanRows 测试）。
func newMockRows(t *testing.T, rows *sqlmock.Rows) *sql.Rows {
	t.Helper()
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	rs, err := db.Query("SELECT")
	assert.NoError(t, err)
	t.Cleanup(func() {
		_ = rs.Close()
		_ = db.Close()
	})
	return rs
}

func TestScanRowsColumnsAndRows(t *testing.T) {
	rows := newMockRows(t,
		sqlmock.NewRows([]string{"id", "name"}).
			AddRow(1, "alice").
			AddRow(nil, "bob"))
	r, err := ScanRows(rows)
	assert.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, r.Columns)
	assert.Equal(t, 2, len(r.Rows))
	// int 列保留为 int（不应被字符串化）
	assert.EqualValues(t, 1, r.Rows[0][0])
	// nil 保留为 nil
	assert.Equal(t, nil, r.Rows[1][0])
}

func TestScanRowsConvertsBytesToString(t *testing.T) {
	// sqlmock 对字符串值默认返回 []byte；若 ScanRows 漏掉 []byte->string 转换，
	// JSON 输出会变成 base64。这里断言转换生效。
	rows := newMockRows(t,
		sqlmock.NewRows([]string{"name"}).AddRow("hello"))
	r, err := ScanRows(rows)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(r.Rows))
	assert.Equal(t, "hello", r.Rows[0][0])
	_, isBytes := r.Rows[0][0].([]byte)
	assert.False(t, isBytes, "[]byte 应已被转成 string")
}

func TestScanRowsEmpty(t *testing.T) {
	rows := newMockRows(t, sqlmock.NewRows([]string{"id", "name"}))
	r, err := ScanRows(rows)
	assert.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, r.Columns)
	assert.Empty(t, r.Rows)
	// 写路径字段不应被本函数设置
	assert.Equal(t, int64(0), r.RowsAffected)
	assert.Equal(t, int64(0), r.LastInsertID)
}

func TestScanRowsRowsErrPropagated(t *testing.T) {
	// 用 RowErrorError 让 rows.Err() 返回错误。
	wantErr := errors.New("driver iteration failed")
	rows := newMockRows(t,
		sqlmock.NewRows([]string{"id"}).
			AddRow(1).
			RowError(0, wantErr))
	_, err := ScanRows(rows)
	assert.ErrorIs(t, err, wantErr)
}
