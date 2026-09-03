package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/query"
	"github.com/AllenMuu/mysql-cli/internal/repl"
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
		// conn.ErrConnFailed 哨兵路径（任务 2）：双 %w 包装后 errors.Is 命中。
		{"conn sentinel wrapped", fmt.Errorf("%w: %w", conn.ErrConnFailed, errors.New("dial tcp: refused")), ExitConnFailed},
		{"conn sentinel direct", conn.ErrConnFailed, ExitConnFailed},
		// 字符串兜底：仅 "dial" 仍命中（驱动直接抛、未经 conn 包装）。
		{"connection dial string fallback", errors.New("dial tcp: connection refused"), ExitConnFailed},
		// 关键回归：SQL error 含 "connection" 字样不再误判为 exit 2，落到 exit 8。
		{"sql error with connection word", fmt.Errorf("%w: bad connection in sql", query.ErrSQL), ExitSQLError},
		// config.ErrConfig 哨兵路径（任务 2）。
		{"config sentinel wrapped", fmt.Errorf("%w: %w", config.ErrConfig, errors.New("unknown datasource")), ExitConfigError},
		{"config fallback", errors.New("unknown datasource"), ExitConfigError},
		// repl.ErrInitFailed 哨兵路径（B4）：readline 初始化失败 -> exit 11，
		// 不再被 "repl exited" 字符串前缀匹配吞成 exit 0。
		{"repl init failed", fmt.Errorf("%w: %w", repl.ErrInitFailed, errors.New("tty boom")), ExitInternalError},
		{"repl init failed bare", repl.ErrInitFailed, ExitInternalError},
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

// TestExitCodeConsistency（任务 4）：遍历 exitCodeHelpHints 表，断言每个退出码
// 在 help.go 的 agentNotesTemplate 里都有对应文本行。这是退出码"行为侧"
// （mapError/errorCodeName）与"文档侧"（--help 输出）一致性的最小保障。
// 新增退出码时若忘了同步 help 模板，本测试会失败。
func TestExitCodeConsistency(t *testing.T) {
	for _, h := range exitCodeHelpHints {
		t.Run(h.Hint, func(t *testing.T) {
			assert.Contains(t, agentNotesTemplate, h.Hint,
				"退出码 %d 的 help 提示 %q 未在 agentNotesTemplate 中找到，请同步更新 help.go", h.Code, h.Hint)
		})
	}
}
