package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/query"
	"github.com/AllenMuu/mysql-cli/internal/safety"
	"github.com/stretchr/testify/assert"
)

// TestExitCodeContract_Precheck 通过真跑 cobra 命令（指向不可达端口，不连库）
// 验证 query 子命令的预检路径返回的退出码。预检在 openPool 之前发生，所以
// 不需要真实 DB。这是任务 2 的核心：覆盖 DDL/destructive/multi-statement
// 的 exit code，弥补 commands_test.go 只测 UPDATE→3 的缺口。
//
// 关键依赖：commands.go 第 174/179 行预检包装使用双 %w
// `fmt.Errorf("%w: %w", query.ErrGuard, err)`，使 mapError 能用 errors.Is
// 同时识别外层 query.ErrGuard 和内层 safety 哨兵。mapError 的检查顺序
// 把 safety 哨兵放在 query.ErrGuard 之前，因此 DDL→4、destructive→5
// 都能正确返回。本测试守护这一修复：若有人回退成 %v（旧 bug），内层
// safety error 不可见，DDL/destructive 会被误判为 readonly(3)，测试即失败。
func TestExitCodeContract_Precheck(t *testing.T) {
	// connArgs 指向不可达端口，确保走到预检就返回（不会真连库）。
	connArgs := []string{"--host", "127.0.0.1", "--port", "1"}

	cases := []struct {
		name     string
		sql      string
		flags    []string
		wantExit int
	}{
		{
			name:     "DDL DROP 无 flag -> exit 4 (DDLNeedsWrite)",
			sql:      "DROP TABLE t",
			flags:    nil,
			wantExit: ExitDDLRequiresWrite,
		},
		{
			name:     "DDL TRUNCATE 无 flag -> exit 4",
			sql:      "TRUNCATE TABLE t",
			flags:    nil,
			wantExit: ExitDDLRequiresWrite,
		},
		{
			name:     "DDL ALTER 无 flag -> exit 4",
			sql:      "ALTER TABLE t ADD COLUMN c INT",
			flags:    nil,
			wantExit: ExitDDLRequiresWrite,
		},
		{
			name:     "DDL DROP 有 --write 无 --ddl -> exit 4",
			sql:      "DROP TABLE t",
			flags:    []string{"--write"},
			wantExit: ExitDDLRequiresWrite,
		},
		{
			name:     "destructive DROP 有 --write --ddl 无 --yes -> exit 5",
			sql:      "DROP TABLE t",
			flags:    []string{"--write", "--ddl"},
			wantExit: ExitDestructiveRequiresYes,
		},
		{
			name:     "destructive DELETE 无 WHERE 有 --write 无 --yes -> exit 5",
			sql:      "DELETE FROM t",
			flags:    []string{"--write"},
			wantExit: ExitDestructiveRequiresYes,
		},
		{
			name:     "destructive UPDATE 无 WHERE 有 --write 无 --yes -> exit 5",
			sql:      "UPDATE t SET a=1",
			flags:    []string{"--write"},
			wantExit: ExitDestructiveRequiresYes,
		},
		{
			name:     "multi-statement -> exit 7",
			sql:      "SELECT 1; SELECT 2",
			flags:    nil,
			wantExit: ExitMultiStatement,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"query", tc.sql}, tc.flags...)
			args = append(args, connArgs...)
			code := Run(args)
			assert.Equal(t, tc.wantExit, code, "sql=%q flags=%v", tc.sql, tc.flags)
		})
	}
}

// TestExitCodeContract_MultiStatement 验证多语句预检稳定返回 exit 7
// （独立于 Precheck 表，确保这个通过预检路径的 case 有明确覆盖）。
func TestExitCodeContract_MultiStatement(t *testing.T) {
	code := Run([]string{"query", "SELECT 1; SELECT 2", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitMultiStatement, code)
}

// TestExitCodeContract_Readonly 验证只读闸门对 DML 的拦截（已知通过的回归基线）。
func TestExitCodeContract_Readonly(t *testing.T) {
	code := Run([]string{"query", "UPDATE t SET a=1 WHERE id=1", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitReadonlyViolation, code)
}

// TestExitCodeContract_MapError 验证 mapError 对各类哨兵 error 的映射正确。
// 这一层测试不需要连库，直接构造 error 验证退出码契约。
// 覆盖 query 包 %w 修复后的映射（ErrSQL/ErrTimeout/ErrGuard），以及 safety
// 哨兵的映射。注意：预检路径的 %v 包装 bug 不影响这里，因为这里直接传
// 未包装的哨兵 error 给 mapError。
func TestExitCodeContract_MapError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		// safety 哨兵 -> 退出码
		{"readonly", safety.ErrReadonlyViolation, ExitReadonlyViolation},
		{"ddl-needs-write", safety.ErrDDLRequiresWrite, ExitDDLRequiresWrite},
		{"destructive-needs-yes", safety.ErrDestructiveRequiresYes, ExitDestructiveRequiresYes},
		{"identifier", safety.ErrIdentifierInvalid, ExitIdentifierInvalid},
		// query 哨兵 -> 退出码（验证 %w 修复后 errors.Is 能识别）
		{"multi-statement", query.ErrMultiStatement, ExitMultiStatement},
		{"sql-error", query.ErrSQL, ExitSQLError},
		{"timeout", query.ErrTimeout, ExitQueryTimeout},
		// query.ErrGuard 映射到 readonly（mapError 第 30-31 行）
		{"guard", query.ErrGuard, ExitReadonlyViolation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mapError(tc.err))
		})
	}
}

// TestExitCodeContract_MapErrorWrapped 验证 mapError 用 errors.Is 穿透 %w 包装。
// 这是任务 2 的关键：query 包已把内部 error 用 fmt.Errorf("%w: %v", ErrXxx, err)
// 包装，mapError 必须能识别内层哨兵。如果 query 包仍用 %v（旧 bug），
// 这些断言会失败——正是本测试要守护的回归点。
func TestExitCodeContract_MapErrorWrapped(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		// 模拟 query.Execute 内部：fmt.Errorf("%w: %v", ErrGuard, safetyErr)
		// 此时 %w 指向 ErrGuard，mapError 应命中 query.ErrGuard 分支 -> 3。
		{"wrapped guard over readonly", fmt.Errorf("%w: %v", query.ErrGuard, safety.ErrReadonlyViolation), ExitReadonlyViolation},
		// query.ExecuteWrite 返回 fmt.Errorf("%w: %v", ErrSQL, driverErr) -> 8
		{"wrapped sql", fmt.Errorf("%w: %v", query.ErrSQL, errors.New("driver boom")), ExitSQLError},
		// query.Execute 返回 fmt.Errorf("%w: %v", ErrTimeout, ctxErr) -> 9
		{"wrapped timeout", fmt.Errorf("%w: %v", query.ErrTimeout, errors.New("context deadline exceeded")), ExitQueryTimeout},
		// 直接的 safety 哨兵（未包装）-> 对应退出码
		{"bare ddl", safety.ErrDDLRequiresWrite, ExitDDLRequiresWrite},
		{"bare destructive", safety.ErrDestructiveRequiresYes, ExitDestructiveRequiresYes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mapError(tc.err))
		})
	}
}

// TestExitCodeContract_MapErrorWrappedSafetyInsideGuard 反向守护：
// 若有人把 commands.go 第 179 行的双 %w 回退成 `%w: %v`，内层 safety 哨兵
// 会被字符串化，errors.Is 无法穿透，mapError 只能命中 query.ErrGuard → 3，
// DDL/destructive 就会被误判为 readonly。本测试构造 %v 包装，断言这种
// 情况下 errors.Is 失败且 mapError 返回 3，作为「%v 是 bug 根源」的回归
// 基线。commands.go 当前用双 %w，正常路径不会走到这里；该测试存在的
// 意义是一旦回退，Precheck 表的 DDL/destructive case 会先失败暴露问题。
func TestExitCodeContract_MapErrorWrappedSafetyInsideGuard(t *testing.T) {
	// %w 包 ErrGuard，safety error 用 %v 字符串化 -> errors.Is(safety.*) 为 false
	err := fmt.Errorf("%w: %v", query.ErrGuard, safety.ErrDDLRequiresWrite)
	// 这种（错误的）包装下 mapError 命中 query.ErrGuard -> ExitReadonlyViolation(3)
	assert.Equal(t, ExitReadonlyViolation, mapError(err),
		"%v 包装使 DDL 错误被误判为 readonly(3)；双 %w 修复后应返回 4")

	// 确认 errors.Is 无法穿透 %v 到达内层 safety 哨兵（bug 根源的回归守护）
	assert.False(t, errors.Is(err, safety.ErrDDLRequiresWrite),
		"%v 包装使 errors.Is 失败，这正是 bug 根源")
}
