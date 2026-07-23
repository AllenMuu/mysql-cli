package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/AllenMuu/mysql-cli/internal/format"
	"github.com/AllenMuu/mysql-cli/internal/query"
	"github.com/AllenMuu/mysql-cli/internal/safety"
)

// mapError translates a core error into an exit code.
func mapError(err error) int {
	switch {
	case errors.Is(err, ErrInitAllFailed):
		return ExitInitFailed
	case errors.Is(err, safety.ErrReadonlyViolation):
		return ExitReadonlyViolation
	case errors.Is(err, safety.ErrDDLRequiresWrite):
		return ExitDDLRequiresWrite
	case errors.Is(err, safety.ErrDestructiveRequiresYes):
		return ExitDestructiveRequiresYes
	case errors.Is(err, safety.ErrIdentifierInvalid):
		return ExitIdentifierInvalid
	case errors.Is(err, query.ErrMultiStatement):
		return ExitMultiStatement
	case errors.Is(err, query.ErrTimeout):
		return ExitQueryTimeout
	case errors.Is(err, query.ErrSQL):
		return ExitSQLError
	case errors.Is(err, query.ErrGuard):
		return ExitReadonlyViolation
	}
	// connection / config failures
	msg := err.Error()
	if strings.Contains(msg, "dial") || strings.Contains(msg, "connection") {
		return ExitConnFailed
	}
	return ExitConfigError
}

// formatErr renders an error in the configured output format.
func formatErr(err error, formatName string) string {
	code := errorCodeName(mapError(err))
	if formatName == "json" || formatName == "" {
		return format.ErrorJSON(code, err.Error())
	}
	return fmt.Sprintf("Error [%s]: %s", code, err.Error())
}

func errorCodeName(code int) string {
	switch code {
	case ExitConnFailed:
		return "CONN_FAILED"
	case ExitReadonlyViolation:
		return "READONLY_VIOLATION"
	case ExitDDLRequiresWrite:
		return "DDL_REQUIRES_WRITE"
	case ExitDestructiveRequiresYes:
		return "DESTRUCTIVE_REQUIRES_YES"
	case ExitIdentifierInvalid:
		return "IDENTIFIER_INVALID"
	case ExitMultiStatement:
		return "MULTI_STATEMENT"
	case ExitSQLError:
		return "SQL_ERROR"
	case ExitQueryTimeout:
		return "QUERY_TIMEOUT"
	case ExitConfigError:
		return "CONFIG_ERROR"
	case ExitInitFailed:
		return "INIT_FAILED"
	}
	return "UNKNOWN"
}
