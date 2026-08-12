// Package conn builds a MySQL DSN from a config.Datasource and opens a
// pooled *sql.DB connection. SSH tunneling is added in a later task.
package conn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/go-sql-driver/mysql"
)

// ErrConnFailed 哨兵 error：所有连接失败（含 SSH 隧道、sql.Open、Ping）
// 都用 %w 包装它，让 cli 层 mapError 用 errors.Is 精确命中 ExitConnFailed，
// 取代脆弱的字符串匹配（"dial"/"connection"）。注意驱动直接抛的、未被本包
// 包装的错误仍走字符串兜底，但本包返回的错误一律走哨兵。
var ErrConnFailed = errors.New("conn: connection failed")

// DSN renders a go-sql-driver/mysql DSN with tls, charset, sql_mode, timeout.
// 使用 mysql.Config + FormatDSN 构造，确保密码等字段正确转义（含 @ / ? # 等特殊字符）。
//
// 默认值（host=localhost / port=3306 / timeout=10 / charset=utf8mb4 / sql_mode=TRADITIONAL）
// 统一由 config.applyDefaults 负责，本函数不再重复兜底——避免改一处忘改另一处漂移。
// 调用方应保证传入的 ds 已经过 Resolve/applyDefaults 处理；仅 timeout 保留兜底，
// 因为 mysql.Config.Timeout=0 会被驱动视为「无超时」，与历史 10s 行为不符，且
// 测试可能直接构造 Datasource 不走 applyDefaults。
func DSN(ds config.Datasource) string {
	timeout := ds.ConnectTimeout
	if timeout <= 0 {
		timeout = 10
	}
	cfg := mysql.Config{
		User:   ds.User,
		Passwd: ds.Password, // FormatDSN 会负责转义，含 @ / ? # 也安全
		Net:    "tcp",
		Addr:   fmt.Sprintf("%s:%d", ds.Host, ds.Port),
		DBName: ds.Database,
		Params: map[string]string{
			"charset": ds.Charset,
		},
		Timeout: time.Duration(timeout) * time.Second,
	}
	if ds.SQLMode != "" {
		cfg.Params["sql_mode"] = ds.SQLMode
	}
	if ds.Collation != "" {
		// 用专门字段而非 Params，确保 FormatDSN 输出 collation 参数。
		cfg.Collation = ds.Collation
	}
	if ds.SSLMode != "" {
		cfg.TLSConfig = ds.SSLMode
	}
	return cfg.FormatDSN()
}

// Pool wraps a *sql.DB for the active datasource.
type Pool struct {
	DB *sql.DB
	// closer, when non-nil, releases SSH tunnel resources (ssh client +
	// local listener) backing the DB connection. It is closed by Close.
	closer io.Closer
}

// Open opens a pooled connection and verifies it with a Ping.
// When SSH is enabled, a tunnel is established first and the DSN points at
// the local forwarded port.
func Open(ctx context.Context, ds config.Datasource) (*Pool, error) {
	return openWithTunnelHook(ctx, ds, defaultTunnelHook)
}

// openWithTunnelHook is the testable form of Open.
func openWithTunnelHook(ctx context.Context, ds config.Datasource, hook tunnelHook) (*Pool, error) {
	effective := ds
	var tunnelCloser io.Closer
	if ds.SSH != nil && ds.SSH.Enable {
		// 把 ds.ConnectTimeout 传给 tunnel hook，让 SSH 拨号超时与 MySQL 一致；
		// establishTunnel 内部对 <=0 回落默认 10s。
		host, port, closer, err := hook(ds.SSH, ds.ConnectTimeout)
		if err != nil {
			// SSH 隧道建立失败 -> 连接失败。双 %w 保链，cli 层 errors.Is 可命中。
			return nil, fmt.Errorf("%w: %w", ErrConnFailed, err)
		}
		effective.Host = host
		effective.Port = port
		tunnelCloser = closer
	}
	db, err := sql.Open("mysql", DSN(effective))
	if err != nil {
		if tunnelCloser != nil {
			tunnelCloser.Close()
		}
		return nil, fmt.Errorf("%w: %w", ErrConnFailed, err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	pingTimeout := effective.ConnectTimeout
	if pingTimeout <= 0 {
		pingTimeout = 10
	}
	pingCtx, cancel := context.WithTimeout(ctx, time.Duration(pingTimeout)*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		if tunnelCloser != nil {
			tunnelCloser.Close()
		}
		return nil, fmt.Errorf("%w: %w", ErrConnFailed, err)
	}
	return &Pool{DB: db, closer: tunnelCloser}, nil
}

// Ping verifies the connection is alive.
func (p *Pool) Ping(ctx context.Context) error {
	return p.DB.PingContext(ctx)
}

// Close releases the pool. If a backing SSH tunnel was established, it is
// torn down before the *sql.DB is closed.
func (p *Pool) Close() error {
	if p.closer != nil {
		p.closer.Close()
	}
	return p.DB.Close()
}
