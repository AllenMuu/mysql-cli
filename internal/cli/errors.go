package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/format"
	"github.com/AllenMuu/mysql-cli/internal/query"
	"github.com/AllenMuu/mysql-cli/internal/repl"
	"github.com/AllenMuu/mysql-cli/internal/safety"
)

// 退出码契约（面向 AI agent，不可随意改动）：
//
//	2=conn  3=readonly  4=ddl  5=destructive  6=identifier
//	7=multi  8=sql  9=timeout  10=config  11=internal
//
// 新增/修改退出码时必须同步更新以下三处，否则会破坏 agent 契约：
//  1. 本文件的 Exit* 常量 + mapError switch + errorCodeName switch
//  2. help.go 的 agentNotesTemplate 文本（`mysql-cli --help` 输出）
//  3. skills/*/SKILL.md 文档（agent 安装的 skill 说明）
//
// TestExitCodeConsistency（见 errors_test.go）会遍历 exitCodeHelpHints
// 断言每个退出码在 agentNotesTemplate 里都有对应文本，作为第 1、2 处
// 一致性的最小保障；第 3 处（skill 文档）目前无自动化校验，需人工同步。

// exitCodeHelpHints 把每个 Exit* 常量映射到它应在 agentNotesTemplate 里
// 出现的关键子串。mapError / errorCodeName 是退出码的"行为侧"，
// agentNotesTemplate 是"文档侧"，本表用于一致性测试。
var exitCodeHelpHints = []struct {
	Code int
	Hint string // 必须出现在 agentNotesTemplate 中
}{
	{ExitConnFailed, "2 conn"},
	{ExitReadonlyViolation, "3 readonly"},
	{ExitDDLRequiresWrite, "4 ddl-needs-write"},
	{ExitDestructiveRequiresYes, "5 destructive-needs-yes"},
	{ExitIdentifierInvalid, "6 identifier"},
	{ExitMultiStatement, "7 multi-statement"},
	{ExitSQLError, "8 sql"},
	{ExitQueryTimeout, "9 timeout"},
	{ExitConfigError, "10 config"},
	{ExitInternalError, "11 internal"},
}

// mapError translates a core error into an exit code.
//
// 优先用 errors.Is 命中各包的哨兵 error（精确、可链式），最后才回落到
// 字符串匹配兜底——后者只覆盖驱动直接抛出的、未被 conn 包包装的错误。
// 注意 SQL error 若错误信息含 "connection" 字样，不能误判为 exit 2：
// conn.ErrConnFailed 是 conn 包返回错误的唯一来源，errors.Is 能精确区分。
func mapError(err error) int {
	switch {
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
	case errors.Is(err, conn.ErrConnFailed):
		// conn 包已用 %w: %w 包装 SSH / sql.Open / Ping 错误。
		return ExitConnFailed
	case errors.Is(err, config.ErrConfig):
		// config 包已用 %w: %w 包装 toml / env / placeholder / unknown ds 错误。
		return ExitConfigError
	case errors.Is(err, repl.ErrInitFailed):
		// REPL readline 初始化失败（终端/环境不可用）属内部错误而非用户
		// 配置错误：曾经的字符串前缀匹配（"repl exited"）会把它吞成 exit 0。
		return ExitInternalError
	}
	// 字符串匹配兜底：仅覆盖驱动直接抛出、未经 conn 包包装的错误。
	// 注意只匹配 "dial"（连接拒绝的典型信号），不再匹配泛化的 "connection"
	// 字样，避免 SQL error 含 "connection" 字样被误判为 exit 2。
	msg := err.Error()
	if strings.Contains(msg, "dial") {
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
	case ExitInternalError:
		return "INTERNAL_ERROR"
	}
	return "UNKNOWN"
}
