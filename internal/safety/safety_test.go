package safety

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassify(t *testing.T) {
	cases := map[string]Category{
		"SELECT 1":              CategoryRead,
		"  select * from t":     CategoryRead,
		"SHOW TABLES":           CategoryRead,
		"DESCRIBE users":        CategoryRead,
		"EXPLAIN SELECT 1":      CategoryRead,
		"WITH cte AS (...) x":   CategoryRead,
		"INSERT INTO t VALUES":  CategoryDML,
		"UPDATE t SET a=1":      CategoryDML,
		"DELETE FROM t":         CategoryDML,
		"REPLACE INTO t VALUES": CategoryDML,
		"CREATE TABLE t (id int)": CategoryDDL,
		"ALTER TABLE t ADD c":   CategoryDDL,
		"DROP TABLE t":          CategoryDDL,
		"TRUNCATE TABLE t":      CategoryDDL,
		"RENAME TABLE a TO b":   CategoryDDL,
	}
	for sql, want := range cases {
		assert.Equal(t, want, Classify(sql), sql)
	}
}

func TestCheckReadonlyRejectsDML(t *testing.T) {
	_, err := Check("UPDATE t SET a=1", CheckOptions{})
	assert.ErrorIs(t, err, ErrReadonlyViolation)
}

func TestCheckDMLWithWriteAllowed(t *testing.T) {
	d, err := Check("UPDATE t SET a=1 WHERE id=1", CheckOptions{Write: true})
	assert.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestCheckUnknownRequiresWrite(t *testing.T) {
	_, err := Check("CALL foo()", CheckOptions{})
	assert.ErrorIs(t, err, ErrReadonlyViolation)

	_, err = Check("SET @x=1", CheckOptions{})
	assert.ErrorIs(t, err, ErrReadonlyViolation)

	d, err := Check("CALL foo()", CheckOptions{Write: true})
	assert.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, CategoryUnknown, d.Category)

	d, err = Check("SET @x=1", CheckOptions{Write: true})
	assert.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, CategoryUnknown, d.Category)
}

func TestCheckDDLRequiresDDLFlag(t *testing.T) {
	_, err := Check("DROP TABLE t", CheckOptions{Write: true, Yes: true})
	assert.ErrorIs(t, err, ErrDDLRequiresWrite)

	_, err = Check("DROP TABLE t", CheckOptions{Write: true, DDL: true})
	assert.ErrorIs(t, err, ErrDestructiveRequiresYes)

	d, err := Check("DROP TABLE t", CheckOptions{Write: true, DDL: true, Yes: true})
	assert.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestCheckDestructiveUpdateWithoutWhere(t *testing.T) {
	_, err := Check("UPDATE t SET a=1", CheckOptions{Write: true})
	assert.ErrorIs(t, err, ErrDestructiveRequiresYes)

	d, err := Check("UPDATE t SET a=1 WHERE id=1", CheckOptions{Write: true})
	assert.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestValidateIdentifier(t *testing.T) {
	assert.NoError(t, ValidateIdentifier("users"))
	assert.NoError(t, ValidateIdentifier("t_1$"))
	assert.ErrorIs(t, ValidateIdentifier("users;"), ErrIdentifierInvalid)
	assert.ErrorIs(t, ValidateIdentifier("us ers"), ErrIdentifierInvalid)
	assert.ErrorIs(t, ValidateIdentifier("' OR 1=1"), ErrIdentifierInvalid)
	assert.ErrorIs(t, ValidateIdentifier(""), ErrIdentifierInvalid)
}

func TestValidateQualifiedTable(t *testing.T) {
	db, tbl, err := ValidateQualifiedTable("mydb.users")
	assert.NoError(t, err)
	assert.Equal(t, "mydb", db)
	assert.Equal(t, "users", tbl)

	_, _, err = ValidateQualifiedTable("users")
	assert.NoError(t, err)

	_, _, err = ValidateQualifiedTable("a.b.c")
	assert.ErrorIs(t, err, ErrIdentifierInvalid)
}

func TestHasMultiStatement(t *testing.T) {
	assert.False(t, HasMultiStatement("SELECT 1"))
	assert.False(t, HasMultiStatement("SELECT 1;"))
	assert.True(t, HasMultiStatement("SELECT 1; SELECT 2"))
	assert.True(t, HasMultiStatement("USE db; SELECT 1"))
}

func TestIsDestructive(t *testing.T) {
	assert.True(t, IsDestructive("DROP TABLE t"))
	assert.True(t, IsDestructive("TRUNCATE TABLE t"))
	assert.False(t, IsDestructive("DELETE FROM t WHERE id=1"))
	assert.True(t, IsDestructive("DELETE FROM t"))
}
