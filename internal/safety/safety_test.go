package safety

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassify(t *testing.T) {
	cases := map[string]Category{
		"SELECT 1":          CategoryRead,
		"  select * from t": CategoryRead,
		"SHOW TABLES":       CategoryRead,
		"DESCRIBE users":    CategoryRead,
		"EXPLAIN SELECT 1":  CategoryRead,
		// WITH 开头的语句一律归入 Unknown（见 safety.go 中 readPrefixes 的注释）。
		"WITH cte AS (...) x":     CategoryUnknown,
		"INSERT INTO t VALUES":    CategoryDML,
		"UPDATE t SET a=1":        CategoryDML,
		"DELETE FROM t":           CategoryDML,
		"REPLACE INTO t VALUES":   CategoryDML,
		"CREATE TABLE t (id int)": CategoryDDL,
		"ALTER TABLE t ADD c":     CategoryDDL,
		"DROP TABLE t":            CategoryDDL,
		"TRUNCATE TABLE t":        CategoryDDL,
		"RENAME TABLE a TO b":     CategoryDDL,
	}
	for sql, want := range cases {
		assert.Equal(t, want, Classify(sql), sql)
	}
}

// TestClassifyCTEPrefix regression: CTE 前缀的 DELETE/UPDATE/SELECT 都不能再被
// 误判为只读，否则只读模式下可执行 WITH ... DELETE FROM t 删表。
func TestClassifyCTEPrefix(t *testing.T) {
	cases := map[string]Category{
		// 写操作前缀：必须落入 Unknown，要求 --write。
		"WITH x AS (SELECT 1) DELETE FROM t":    CategoryUnknown,
		"WITH x AS (SELECT 1) UPDATE t SET a=1": CategoryUnknown,
		// 纯只读 CTE-SELECT 也落入 Unknown：方案 B 的保守代价。
		"WITH x AS (SELECT 1) SELECT * FROM x":              CategoryUnknown,
		"WITH RECURSIVE r(n) AS (SELECT 1) SELECT * FROM r": CategoryUnknown,
	}
	for sql, want := range cases {
		assert.Equal(t, want, Classify(sql), sql)
	}
}

// TestCheckCTEPrefixReadonlyRejects 验证只读模式下 CTE-DELETE 被闸门拦截。
func TestCheckCTEPrefixReadonlyRejects(t *testing.T) {
	_, err := Check("WITH x AS (SELECT 1) DELETE FROM t", CheckOptions{})
	assert.ErrorIs(t, err, ErrReadonlyViolation)

	// 加 --write 后仍需 --yes：CTE 前缀的全表 DELETE 与裸 DELETE FROM t
	// 一样是破坏性操作（F1）。
	_, err = Check("WITH x AS (SELECT 1) DELETE FROM t", CheckOptions{Write: true})
	assert.ErrorIs(t, err, ErrDestructiveRequiresYes)

	d, err := Check("WITH x AS (SELECT 1) DELETE FROM t", CheckOptions{Write: true, Yes: true})
	assert.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, CategoryUnknown, d.Category)
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

// TestHasMultiStatementQuoteAware 验证 A9：引号内的分号不算语句分隔符，
// WHERE note='a;b' 不再被误判为多语句。
func TestHasMultiStatementQuoteAware(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		// 字面量内的分号：单语句。
		{sql: "SELECT * FROM t WHERE note='a;b'", want: false},
		{sql: `SELECT "a;b" FROM t`, want: false},
		{sql: "SELECT `a;b` FROM t", want: false},
		{sql: "SELECT 'a'';b' FROM t", want: false},  // '' 双写转义
		{sql: "SELECT 'a\\';b' FROM t", want: false}, // \' 反斜杠转义
		// 引号外的分号：多语句。
		{sql: "SELECT 1; SELECT 2", want: true},
		{sql: "SELECT ';' ; SELECT 2", want: true},
		{sql: "SELECT 'a'; SELECT 2", want: true},
		{sql: "SELECT `a`; SELECT 2", want: true},
		// 尾部容忍单个分号 + 字面量组合。
		{sql: "SELECT 'a;b';", want: false},
		{sql: "SELECT * FROM t WHERE note='a;b';", want: false},
	}
	for _, tt := range cases {
		assert.Equal(t, tt.want, HasMultiStatement(tt.sql), tt.sql)
	}
}

// TestStripLiterals 覆盖 A9/A11 共用的字面量剥离扫描器。
func TestStripLiterals(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "SELECT 'abc' FROM t", want: "SELECT  FROM t"},
		{in: "SELECT `abc` FROM t", want: "SELECT  FROM t"},
		{in: `SELECT "abc" FROM t`, want: "SELECT  FROM t"},
		{in: "SELECT 'a''b' FROM t", want: "SELECT  FROM t"},  // '' 双写
		{in: "SELECT 'a\\'b' FROM t", want: "SELECT  FROM t"}, // \' 转义
		{in: "a;b", want: "a;b"},                              // 引号外原样保留
		{in: "SELECT 'a;b", want: "SELECT "},                  // 未闭合引号：其后全部剥离
	}
	for _, tt := range cases {
		assert.Equal(t, tt.want, StripLiterals(tt.in), tt.in)
	}
}

// TestClassifyExplainAnalyze 验证 A2：MySQL 8.0.18+ 的 EXPLAIN ANALYZE 会
// 实际执行被分析语句（含 DELETE/UPDATE/INSERT），必须归入 CategoryUnknown
// （需 --write），不能按普通 EXPLAIN 归入只读。
func TestClassifyExplainAnalyze(t *testing.T) {
	assert.Equal(t, CategoryUnknown, Classify("EXPLAIN ANALYZE DELETE FROM t WHERE id=1"))
	assert.Equal(t, CategoryUnknown, Classify("explain analyze update t set a=1"))
	assert.Equal(t, CategoryUnknown, Classify("  EXPLAIN ANALYZE INSERT INTO t VALUES (1)"))
	// 普通 EXPLAIN 仍是只读。
	assert.Equal(t, CategoryRead, Classify("EXPLAIN SELECT 1"))
	assert.Equal(t, CategoryRead, Classify("EXPLAIN DELETE FROM t"))
}

// TestCheckExplainAnalyzeReadonlyRejects 验证只读模式下 EXPLAIN ANALYZE 被
// 闸门拦截，加 --write --yes 后放行（EXPLAIN ANALYZE 会实际执行全表 DELETE，
// F1）。
func TestCheckExplainAnalyzeReadonlyRejects(t *testing.T) {
	_, err := Check("EXPLAIN ANALYZE DELETE FROM t", CheckOptions{})
	assert.ErrorIs(t, err, ErrReadonlyViolation)

	_, err = Check("EXPLAIN ANALYZE DELETE FROM t", CheckOptions{Write: true})
	assert.ErrorIs(t, err, ErrDestructiveRequiresYes)

	d, err := Check("EXPLAIN ANALYZE DELETE FROM t", CheckOptions{Write: true, Yes: true})
	assert.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, CategoryUnknown, d.Category)
}

// TestCheckUnknownDestructive（F1）：unknown 类语句（WITH CTE / EXPLAIN
// ANALYZE 前缀）的语句体含 DELETE FROM / UPDATE ... SET 动词时，与裸
// DELETE FROM t 一样要求 --write --yes。
func TestCheckUnknownDestructive(t *testing.T) {
	destructive := []string{
		"EXPLAIN ANALYZE DELETE FROM t",
		"EXPLAIN ANALYZE UPDATE t SET a=1",
		"explain analyze delete from t",
		"WITH c AS (SELECT 1) DELETE FROM t",
		"WITH c AS (SELECT 1) UPDATE t SET a=1",
		"WITH RECURSIVE r(n) AS (SELECT 1) DELETE FROM t",
		// CTE 体内的 WHERE 不能豁免最终的 DELETE：这是全表删除。
		"WITH x AS (SELECT * FROM src WHERE id=1) DELETE FROM t",
		// 定向 DELETE 也要求 --yes：unknown 场景下 WHERE 归属无法可靠判定，
		// 宁可误拦（与裸 DELETE ... WHERE 的 DML 语义不同）。
		"WITH c AS (SELECT 1) DELETE FROM t WHERE id=1",
	}
	for _, sql := range destructive {
		_, err := Check(sql, CheckOptions{Write: true})
		assert.ErrorIs(t, err, ErrDestructiveRequiresYes, sql)
		d, err := Check(sql, CheckOptions{Write: true, Yes: true})
		assert.NoError(t, err, sql)
		assert.True(t, d.Allowed, sql)
	}

	nonDestructive := []string{
		// CTE-SELECT：无破坏性动词，--write 即可。
		"WITH c AS (SELECT 1) SELECT * FROM c",
		"WITH RECURSIVE r(n) AS (SELECT 1) SELECT * FROM r",
		// EXPLAIN ANALYZE 只读 SELECT：无破坏性动词。
		"EXPLAIN ANALYZE SELECT * FROM t",
		// 一般 unknown 语句不受影响。
		"CALL foo()",
		"SET @x=1",
		// 字面量里的破坏性动词不算（StripLiterals）。
		"WITH c AS (SELECT 'DELETE FROM t') SELECT * FROM c",
		`SET @note = 'update t set a=1'`,
		// 标识符含 update/delete 字样但不构成独立动词。
		"SET @last_update = (SELECT updated_at FROM t)",
		// INSERT（含 WITH 前缀）不是破坏性动词，与裸 INSERT 语义一致。
		"WITH c AS (SELECT 1) INSERT INTO t VALUES (1)",
	}
	for _, sql := range nonDestructive {
		d, err := Check(sql, CheckOptions{Write: true})
		assert.NoError(t, err, sql)
		assert.True(t, d.Allowed, sql)
	}
}

// TestFirstKeywordCRLF 验证 A10：CRLF 输入的首词不被切成 "SELECT\r"。
func TestFirstKeywordCRLF(t *testing.T) {
	assert.Equal(t, "SELECT", firstKeyword("SELECT\r\n1"))
	assert.Equal(t, CategoryRead, Classify("SELECT\r\n * FROM t"))
	assert.Equal(t, CategoryDML, Classify("UPDATE\r\nt SET a=1"))
	assert.Equal(t, CategoryRead, Classify("SHOW\r\nTABLES"))
}

func TestIsDestructive(t *testing.T) {
	assert.True(t, IsDestructive("DROP TABLE t"))
	assert.True(t, IsDestructive("TRUNCATE TABLE t"))
	assert.False(t, IsDestructive("DELETE FROM t WHERE id=1"))
	assert.True(t, IsDestructive("DELETE FROM t"))
}
