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
	readPrefixes   = []string{"SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "WITH"}
	dmlPrefixes    = []string{"INSERT", "UPDATE", "DELETE", "REPLACE"}
	ddlPrefixes    = []string{"CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME"}
	destructiveRe  = regexp.MustCompile(`(?i)^\s*(DROP|TRUNCATE)\b`)
	deleteUpdateRe = regexp.MustCompile(`(?i)^\s*(DELETE|UPDATE)\b`)
	whereRe        = regexp.MustCompile(`(?i)\bWHERE\b`)
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

func firstWord(sql string) string {
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
	w := firstWord(sql)
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

// Check enforces the gate: read always allowed; DML needs Write and, if
// destructive, Yes; DDL needs Write+DDL and, if destructive, Yes.
func Check(sql string, opts CheckOptions) (*Decision, error) {
	cat := Classify(sql)
	switch cat {
	case CategoryRead, CategoryUnknown:
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