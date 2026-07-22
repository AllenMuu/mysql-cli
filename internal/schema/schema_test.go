package schema

import (
	"context"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func newMock(t *testing.T) (*conn.Pool, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	return &conn.Pool{DB: db}, mock
}

func TestDatabases(t *testing.T) {
	pool, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"Database"}).AddRow("app").AddRow("mysql")
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(rows)
	r, err := Databases(context.Background(), pool)
	assert.NoError(t, err)
	// system dbs filtered
	assert.Equal(t, 1, len(r.Rows))
	assert.Equal(t, "app", r.Rows[0][0])
}

func TestTables(t *testing.T) {
	pool, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"Tables_in_test"}).AddRow("users").AddRow("orders")
	mock.ExpectQuery("SHOW TABLES").WillReturnRows(rows)
	r, err := Tables(context.Background(), pool, "test")
	assert.NoError(t, err)
	assert.Equal(t, 2, len(r.Rows))
}

func TestTablesEmptyDB(t *testing.T) {
	pool, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"Tables_in_app"}).AddRow("users")
	mock.ExpectQuery("SHOW TABLES$").WillReturnRows(rows)
	r, err := Tables(context.Background(), pool, "")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(r.Rows))
}

func TestTablesBadDB(t *testing.T) {
	pool, _ := newMock(t)
	_, err := Tables(context.Background(), pool, "bad;db")
	assert.Error(t, err)
}

func TestSchemaSingleTable(t *testing.T) {
	pool, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT"}).
		AddRow("id", "int", "NO", nil, "")
	mock.ExpectQuery("FROM information_schema.COLUMNS").WillReturnRows(rows)
	r, err := Schema(context.Background(), pool, "users")
	assert.NoError(t, err)
	assert.Equal(t, []string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT"}, r.Columns)
}

func TestSchemaAllTables(t *testing.T) {
	pool, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"TABLE_NAME", "COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"}).
		AddRow("users", "id", "int", "NO")
	mock.ExpectQuery("TABLE_SCHEMA = DATABASE").WillReturnRows(rows)
	r, err := Schema(context.Background(), pool, "")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(r.Rows))
}

func TestSchemaQualifiedTable(t *testing.T) {
	pool, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT"}).
		AddRow("id", "int", "NO", nil, "")
	mock.ExpectQuery("TABLE_SCHEMA = 'mydb'").WillReturnRows(rows)
	r, err := Schema(context.Background(), pool, "mydb.users")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(r.Rows))
}

func TestSampleLimitClamped(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM `users` LIMIT 20").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	_, err := Sample(context.Background(), pool, "users", 50)
	assert.NoError(t, err)
}

func TestSampleLimitMax20(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM `users` LIMIT 20").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	_, err := Sample(context.Background(), pool, "users", 100)
	assert.NoError(t, err)
}

func TestSampleQualifiedTable(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM `mydb`.`users` LIMIT 5").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	_, err := Sample(context.Background(), pool, "mydb.users", 5)
	assert.NoError(t, err)
}

func TestSampleBadIdentifier(t *testing.T) {
	pool, _ := newMock(t)
	_, err := Sample(context.Background(), pool, "users; DROP", 5)
	assert.Error(t, err)
}

func TestSampleDefaultLimit(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM `users` LIMIT 5").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	_, err := Sample(context.Background(), pool, "users", 0)
	assert.NoError(t, err)
}

func TestReadLimit100(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM `users` LIMIT 100").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	_, err := Read(context.Background(), pool, "users")
	assert.NoError(t, err)
}

func TestReadQualifiedTable(t *testing.T) {
	pool, mock := newMock(t)
	mock.ExpectQuery("FROM `mydb`.`users` LIMIT 100").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	_, err := Read(context.Background(), pool, "mydb.users")
	assert.NoError(t, err)
}

func TestReadBadIdentifier(t *testing.T) {
	pool, _ := newMock(t)
	_, err := Read(context.Background(), pool, "users; DROP")
	assert.Error(t, err)
}
