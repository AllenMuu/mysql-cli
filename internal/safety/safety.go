// Package safety classifies SQL statements, enforces the read-only/write/ddl
// gate, validates identifiers, detects multi-statement input, and flags
// destructive operations. It is dependency-free and fully unit-testable.
package safety

import (
	"errors"
	"regexp"
	"strings"
)

// Category is the safety classification of a SQL statement.
type Category int

const (
	CategoryUnknown Category = iota
	CategoryRead
	CategoryDML
	CategoryDDL
)

// Sentinel errors. Each maps to a CLI exit code in the cli layer.
var (
	ErrReadonlyViolation      = errors.New("statement requires --write")
	ErrDDLRequiresWrite       = errors.New("ddl requires --write and --ddl")
	ErrDestructiveRequiresYes = errors.New("destructive operation requires --yes")
	ErrIdentifierInvalid      = errors.New("invalid identifier")
	ErrMultiStatement         = errors.New("multiple statements are not allowed; use the txn subcommand")
)

var (
	identifierRe = regexp.MustCompile(`^[a-zA-Z0-9_$]+$`)
	// 注意：WITH 不在 readPrefixes 中。MySQL 8 支持 CTE 前缀的写操作
	// （WITH ... DELETE FROM t / WITH ... UPDATE t SET ...），仅凭首词
	// 无法区分 CTE-SELECT 与 CTE-DELETE/UPDATE。为避免在只读模式下放行
	// CTE-DELETE/UPDATE，保守地将所有 WITH 开头的语句归入 CategoryUnknown，
	// 要求 --write 才能执行。代价是纯只读的 CTE-SELECT 也需要 --write，
	// 但这比绕过只读闸门删表安全得多。
	//
	// EXPLAIN 也有同样的坑：MySQL 8.0.18+ 的 EXPLAIN ANALYZE 会实际执行被
	// 分析的语句（含 DELETE/UPDATE/INSERT），不能按普通 EXPLAIN 归入只读，
	// 否则只读闸门和 --yes 防线都会被绕过。Classify 中用 explainAnalyzeRe
	// 先于 readPrefixes 特判，统一归入 CategoryUnknown。
	readPrefixes     = []string{"SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN"}
	explainAnalyzeRe = regexp.MustCompile(`(?i)^\s*EXPLAIN\s+ANALYZE\b`)
	dmlPrefixes      = []string{"INSERT", "UPDATE", "DELETE", "REPLACE"}
	ddlPrefixes      = []string{"CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME"}
	destructiveRe    = regexp.MustCompile(`(?i)^\s*(DROP|TRUNCATE)\b`)
	deleteUpdateRe   = regexp.MustCompile(`(?i)^\s*(DELETE|UPDATE)\b`)
	// whereRe 仅做 best-effort 检测：只判断 WHERE 关键字是否存在，不分析
	// WHERE 条件是否恒真（如 WHERE 1=1 / WHERE TRUE）。恒真条件的全表删除
	// 会被 IsDestructive 误判为非破坏性，但仍需 --write 才能执行。这是有意的
	// 保守简化，避免实现 SQL 语义分析。
	whereRe = regexp.MustCompile(`(?i)\bWHERE\b`)
)

// CheckOptions carries the user's explicit permission flags.
type CheckOptions struct {
	Write bool
	DDL   bool
	Yes   bool
}

// Decision is the outcome of Check.
type Decision struct {
	Allowed  bool
	Category Category
}

func firstKeyword(sql string) string {
	s := strings.TrimSpace(sql)
	for i, r := range s {
		// '\r' 必须算分隔符：CRLF 输入的首词否则会被切成 "SELECT\r"，
		// 前缀匹配全部落空。
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' {
			return strings.ToUpper(s[:i])
		}
	}
	return strings.ToUpper(s)
}

// Classify categorizes a SQL statement by its leading keyword.
func Classify(sql string) Category {
	// EXPLAIN ANALYZE 特判先于 readPrefixes：MySQL 8.0.18+ 会实际执行被
	// 分析的语句，归入 CategoryUnknown（需 --write）。
	if explainAnalyzeRe.MatchString(sql) {
		return CategoryUnknown
	}
	w := firstKeyword(sql)
	for _, p := range readPrefixes {
		if w == p {
			return CategoryRead
		}
	}
	for _, p := range dmlPrefixes {
		if w == p {
			return CategoryDML
		}
	}
	for _, p := range ddlPrefixes {
		if w == p {
			return CategoryDDL
		}
	}
	return CategoryUnknown
}

// IsDestructive reports whether a statement is DROP/TRUNCATE or an
// UPDATE/DELETE without a WHERE clause.
func IsDestructive(sql string) bool {
	if destructiveRe.MatchString(sql) {
		return true
	}
	if deleteUpdateRe.MatchString(sql) && !whereRe.MatchString(sql) {
		return true
	}
	return false
}

// Check enforces the gate: read always allowed; unknown statements need Write;
// DML needs Write and, if destructive, Yes; DDL needs Write+DDL and, if
// destructive, Yes.
func Check(sql string, opts CheckOptions) (*Decision, error) {
	cat := Classify(sql)
	switch cat {
	case CategoryRead:
		return &Decision{Allowed: true, Category: cat}, nil
	case CategoryUnknown:
		if !opts.Write {
			return nil, ErrReadonlyViolation
		}
		return &Decision{Allowed: true, Category: cat}, nil
	case CategoryDML:
		if !opts.Write {
			return nil, ErrReadonlyViolation
		}
		if IsDestructive(sql) && !opts.Yes {
			return nil, ErrDestructiveRequiresYes
		}
		return &Decision{Allowed: true, Category: cat}, nil
	case CategoryDDL:
		if !opts.Write || !opts.DDL {
			return nil, ErrDDLRequiresWrite
		}
		if IsDestructive(sql) && !opts.Yes {
			return nil, ErrDestructiveRequiresYes
		}
		return &Decision{Allowed: true, Category: cat}, nil
	}
	return nil, ErrReadonlyViolation
}

// ValidateIdentifier ensures a bare identifier matches the allowlist.
func ValidateIdentifier(name string) error {
	if !identifierRe.MatchString(name) {
		return ErrIdentifierInvalid
	}
	return nil
}

// ValidateQualifiedTable accepts "table" or "database.table" and returns
// the db (possibly empty) and table parts.
func ValidateQualifiedTable(name string) (string, string, error) {
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return "", "", ErrIdentifierInvalid
	}
	for _, p := range parts {
		if err := ValidateIdentifier(p); err != nil {
			return "", "", err
		}
	}
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return "", parts[0], nil
}

// HasMultiStatement reports whether sql contains more than one statement,
// matching the original MCP's trailing-semicolon-tolerant check.
// 引号感知：单引号/双引号/反引号内的分号不算语句分隔符（支持反斜杠转义与
// ” 双写转义），因此 WHERE note='a;b' 不会误判为多语句。
func HasMultiStatement(sql string) bool {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sql), ";"))
	return strings.Contains(StripLiterals(trimmed), ";")
}

// StripLiterals 返回移除字符串字面量与反引号标识符内容后的 SQL 文本
// （引号字符本身一并移除）。供依赖纯文本启发式匹配的调用方使用（如
// query 包的 LIMIT 子句检测），避免字面量内的关键字被误匹配。
// 处理反斜杠转义与 ” / "" 双写转义；反引号内不解析转义（与 MySQL 一致）。
// 未闭合的引号：其后内容全部视为字面量（交给驱动/服务端拒绝语法错误，
// 不会造成多语句绕过——真正的分隔符在引号外仍会被识别）。
func StripLiterals(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))
	var quote byte // 0 = 不在引号内；否则为 '\'' / '"' / '`'
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if quote == 0 {
			switch c {
			case '\'', '"', '`':
				quote = c
			default:
				b.WriteByte(c)
			}
			continue
		}
		if quote == '`' {
			if c == '`' {
				quote = 0
			}
			continue
		}
		if c == '\\' && i+1 < len(sql) {
			i++ // 跳过被转义的字符
			continue
		}
		if c == quote {
			if i+1 < len(sql) && sql[i+1] == quote {
				i++ // '' / "" 双写转义：仍在字面量内
				continue
			}
			quote = 0
		}
	}
	return b.String()
}
