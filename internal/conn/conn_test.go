package conn

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
)

func TestDSN(t *testing.T) {
	ds := config.Datasource{
		Host: "127.0.0.1", Port: 3306, User: "root",
		Password: "secret", Database: "test",
		Charset: "utf8mb4", SQLMode: "TRADITIONAL",
	}
	dsn := DSN(ds)
	assert.Contains(t, dsn, "root:secret@tcp(127.0.0.1:3306)/test")
	assert.Contains(t, dsn, "charset=utf8mb4")
	assert.Contains(t, dsn, "sql_mode=TRADITIONAL")
}

func TestDSNNoDB(t *testing.T) {
	ds := config.Datasource{Host: "h", Port: 3306, User: "u", Password: "p"}
	dsn := DSN(ds)
	assert.Contains(t, dsn, "tcp(h:3306)/")
}

// TestDSNDefaults 验证 DSN 在「已 applyDefaults」的 Datasource 上的输出。
// 注意：默认值填充由 config.applyDefaults 负责，conn.DSN 不再重复兜底
// （修复 #19 双写漂移）。本测试传入模拟 applyDefaults 后的状态。
func TestDSNDefaults(t *testing.T) {
	// 模拟 config.applyDefaults 处理后的 Datasource：
	// host=localhost / port=3306 / connect_timeout=10 / charset=utf8mb4。
	ds := config.Datasource{
		User: "u", Password: "p",
		Host:           "localhost",
		Port:           3306,
		ConnectTimeout: 10,
		Charset:        "utf8mb4",
	}
	dsn := DSN(ds)
	assert.Contains(t, dsn, "tcp(localhost:3306)")
	assert.Contains(t, dsn, "timeout=10s")
	assert.Contains(t, dsn, "charset=utf8mb4")
}

// TestDSNTimeoutFallbackOnZero 验证即使 ConnectTimeout=0（未走 applyDefaults 的
// 直构场景），DSN 仍回落 10s——因为驱动把 Timeout=0 当作「无超时」，与历史行为不符。
// 这是 conn.DSN 唯一保留的兜底（其余默认值已下沉到 config.applyDefaults）。
func TestDSNTimeoutFallbackOnZero(t *testing.T) {
	ds := config.Datasource{Host: "h", Port: 3306, User: "u", Password: "p", Charset: "utf8mb4"}
	dsn := DSN(ds)
	assert.Contains(t, dsn, "timeout=10s")
}

func TestDSNCollation(t *testing.T) {
	ds := config.Datasource{Host: "h", Port: 3306, User: "u", Password: "p", Collation: "utf8mb4_bin"}
	dsn := DSN(ds)
	assert.Contains(t, dsn, "collation=utf8mb4_bin")
}

func TestDSNSSLMode(t *testing.T) {
	ds := config.Datasource{Host: "h", Port: 3306, User: "u", Password: "p", SSLMode: "REQUIRED"}
	dsn := DSN(ds)
	assert.Contains(t, dsn, "tls=REQUIRED")
}

func TestDSNCustomCharset(t *testing.T) {
	ds := config.Datasource{Host: "h", Port: 3306, User: "u", Password: "p", Charset: "utf8"}
	dsn := DSN(ds)
	assert.Contains(t, dsn, "charset=utf8")
}

func TestDSNCustomTimeout(t *testing.T) {
	ds := config.Datasource{Host: "h", Port: 3306, User: "u", Password: "p", ConnectTimeout: 5}
	dsn := DSN(ds)
	assert.Contains(t, dsn, "timeout=5s")
}

func TestOpenPings(t *testing.T) {
	// Use a closed listener to force a fast connection failure.
	ds := config.Datasource{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", ConnectTimeout: 1}
	_, err := Open(context.Background(), ds)
	assert.Error(t, err)
}

// TestOpenWrapsConnFailedSentinel（任务 2）：Open 返回的连接错误必须挂
// ErrConnFailed 哨兵，让 cli 层 mapError 用 errors.Is 精确命中 ExitConnFailed，
// 取代脆弱的字符串匹配。
func TestOpenWrapsConnFailedSentinel(t *testing.T) {
	ds := config.Datasource{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", ConnectTimeout: 1}
	_, err := Open(context.Background(), ds)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrConnFailed, "Open 返回的连接错误必须包装 ErrConnFailed 哨兵")
}

// TestErrConnFailedSentinelDefined 验证哨兵 error 存在且消息稳定（防止被误改）。
func TestErrConnFailedSentinelDefined(t *testing.T) {
	assert.NotNil(t, ErrConnFailed)
	assert.Equal(t, "conn: connection failed", ErrConnFailed.Error())
	// errors.Is 自检：双 %w 包装后能命中外层哨兵和内层 error（Go 1.20+ 多 %w 保链）。
	wrapped := errors.New("dial tcp: connection refused")
	chain := fmt.Errorf("%w: %w", ErrConnFailed, wrapped)
	assert.True(t, errors.Is(chain, ErrConnFailed))
	assert.True(t, errors.Is(chain, wrapped), "内层 error 也应通过 %w 保链被识别")
}

// TestDSNSpecialCharPassword 验证密码含 @ / ? # 等特殊字符时 DSN 能被 ParseDSN 正确解析回来。
// 这是任务 2 的核心：原 fmt.Sprintf 拼接会破坏 DSN，改用 mysql.Config.FormatDSN 后应安全。
func TestDSNSpecialCharPassword(t *testing.T) {
	cases := []string{
		"p@ssw0rd",
		"p/w0rd",
		"p?w0rd",
		"p#w0rd",
		"p@ss?w0/rd#x",
		"复杂密码!@#$%^&*()",
	}
	for _, pw := range cases {
		t.Run(pw, func(t *testing.T) {
			ds := config.Datasource{
				Host: "127.0.0.1", Port: 3306, User: "root",
				Password: pw, Database: "test",
			}
			dsn := DSN(ds)
			// DSN 必须能被驱动解析回来，且密码字段保持原值。
			parsed, err := mysql.ParseDSN(dsn)
			assert.NoError(t, err, "DSN 解析失败，密码可能未正确转义: %s", dsn)
			assert.Equal(t, pw, parsed.Passwd)
			assert.Equal(t, "root", parsed.User)
			assert.Equal(t, "127.0.0.1:3306", parsed.Addr)
			assert.Equal(t, "test", parsed.DBName)
		})
	}
}

// TestDSNPasswordDoesNotBreakFormat 验证含 @ 的密码不会让 DSN 的 host/db 部分错位。
// 原 sprintf 实现下，密码 "p@ss" 会让 DSN 被解析成 user=p : passwd=ss @tcp(...)。
func TestDSNPasswordDoesNotBreakFormat(t *testing.T) {
	ds := config.Datasource{
		Host: "127.0.0.1", Port: 3306, User: "root",
		Password: "p@ssw0rd", Database: "testdb",
	}
	dsn := DSN(ds)
	parsed, err := mysql.ParseDSN(dsn)
	assert.NoError(t, err)
	assert.Equal(t, "root", parsed.User)
	assert.Equal(t, "p@ssw0rd", parsed.Passwd)
	assert.Equal(t, "127.0.0.1:3306", parsed.Addr)
	assert.Equal(t, "testdb", parsed.DBName)
	// 关键：host 仍是 127.0.0.1，没有被密码里的 @ 截断成别的值。
	assert.Contains(t, dsn, "tcp(127.0.0.1:3306)")
	assert.Contains(t, dsn, "/testdb")
}

// TestDSNParamsPreservedWithSpecialPassword 验证特殊字符密码下 query 参数仍正确。
func TestDSNParamsPreservedWithSpecialPassword(t *testing.T) {
	ds := config.Datasource{
		Host: "h", Port: 3306, User: "u", Password: "p@ss?w0rd",
		Charset: "utf8mb4", SQLMode: "TRADITIONAL", Collation: "utf8mb4_bin",
		ConnectTimeout: 5,
	}
	dsn := DSN(ds)
	parsed, err := mysql.ParseDSN(dsn)
	assert.NoError(t, err)
	assert.Equal(t, "utf8mb4", parsed.Params["charset"])
	assert.Equal(t, "TRADITIONAL", parsed.Params["sql_mode"])
	// collation 走 mysql.Config.Collation 专门字段，ParseDSN 后落在 parsed.Collation。
	assert.Equal(t, "utf8mb4_bin", parsed.Collation)
}
