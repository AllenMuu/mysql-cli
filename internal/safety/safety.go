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
	ErrReadonlyViolation         = errors.New("statement requires --write")
	ErrDDLRequiresWrite          = errors.New("ddl requires --write and --ddl")
	ErrDestructiveRequiresYes    = errors.New("destructive operation requires --yes")
	ErrIdentifierInvalid         = errors.New("invalid identifier")
	ErrMultiStatement            = errors.New("multiple statements are not allowed; use the txn subcommand")
)

var (
	identifierRe   = regexp.MustCompile(`^[a-zA-Z0-9_$]+$`)
	// 注意：WITH 不在 readPrefixes 中。MySQL 8 支持 CTE 前缀的写操作
	// （WITH ... DELETE FROM t / WITH ... UPDATE t SET ...），仅凭首词
	// 无法区分 CTE-SELECT 与 CTE-DELETE/UPDATE。为避免在只读模式下放行
	// CTE-DELETE/UPDATE，保守地将所有 WITH 开头的语句归入 CategoryUnknown，
	// 要求 --write 才能执行。代价是纯只读的 CTE-SELECT 也需要 --write，
	// 但这比绕过只读闸门删表安全得多。
	readPrefixes   = []string{"SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN"}
	dmlPrefixes    = []string{"INSERT", "UPDATE", "DELETE", "REPLACE"}
	ddlPrefixes    = []string{"CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME"}
	destructiveRe  = regexp.MustCompile(`(?i)^\s*(DROP|TRUNCATE)\b`)
	deleteUpdateRe = regexp.MustCompile(`(?i)^\s*(DELETE|UPDATE)\b`)
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
		if r == ' ' || r == '\t' || r == '\n' || r == '(' {
			return strings.ToUpper(s[:i])
		}
	}
	return strings.ToUpper(s)
}

// Classify categorizes a SQL statement by its leading keyword.
func Classify(sql string) Category {
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
func HasMultiStatement(sql string) bool {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sql), ";"))
	return strings.Contains(trimmed, ";")
}
