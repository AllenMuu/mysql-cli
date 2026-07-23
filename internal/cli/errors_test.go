package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/query"
	"github.com/AllenMuu/mysql-cli/internal/safety"
	"github.com/stretchr/testify/assert"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"readonly", safety.ErrReadonlyViolation, ExitReadonlyViolation},
		{"ddl", safety.ErrDDLRequiresWrite, ExitDDLRequiresWrite},
		{"destructive", safety.ErrDestructiveRequiresYes, ExitDestructiveRequiresYes},
		{"identifier", safety.ErrIdentifierInvalid, ExitIdentifierInvalid},
		{"multi-statement", query.ErrMultiStatement, ExitMultiStatement},
		{"timeout", query.ErrTimeout, ExitQueryTimeout},
		{"sql", query.ErrSQL, ExitSQLError},
		{"guard", query.ErrGuard, ExitReadonlyViolation},
		{"connection dial", errors.New("dial tcp: connection refused"), ExitConnFailed},
		{"connection msg", errors.New("broken connection"), ExitConnFailed},
		{"config fallback", errors.New("unknown datasource"), ExitConfigError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mapError(tt.err))
		})
	}
}

func TestFormatErrJSON(t *testing.T) {
	out := formatErr(fmt.Errorf("%w: no --write", safety.ErrReadonlyViolation), "json")
	assert.Contains(t, out, `"code":"READONLY_VIOLATION"`)
	assert.Contains(t, out, `"message":"statement requires --write: no --write"`)
}

func TestFormatErrText(t *testing.T) {
	out := formatErr(errors.New("boom"), "table")
	assert.Equal(t, "Error [CONFIG_ERROR]: boom", out)
}

func TestErrorCodeName(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{ExitOK, "UNKNOWN"},
		{ExitConnFailed, "CONN_FAILED"},
		{ExitReadonlyViolation, "READONLY_VIOLATION"},
		{ExitDDLRequiresWrite, "DDL_REQUIRES_WRITE"},
		{ExitDestructiveRequiresYes, "DESTRUCTIVE_REQUIRES_YES"},
		{ExitIdentifierInvalid, "IDENTIFIER_INVALID"},
		{ExitMultiStatement, "MULTI_STATEMENT"},
		{ExitSQLError, "SQL_ERROR"},
		{ExitQueryTimeout, "QUERY_TIMEOUT"},
		{ExitConfigError, "CONFIG_ERROR"},
		{999, "UNKNOWN"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, errorCodeName(tc.code))
	}
}
