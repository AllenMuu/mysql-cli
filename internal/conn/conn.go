// Package conn builds a MySQL DSN from a config.Datasource and opens a
// pooled *sql.DB connection. SSH tunneling is added in a later task.
package conn

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	"github.com/AllenMuu/mysql-cli/internal/config"
	_ "github.com/go-sql-driver/mysql"
)

// DSN renders a go-sql-driver/mysql DSN with tls, charset, sql_mode, timeout.
func DSN(ds config.Datasource) string {
	host := ds.Host
	if host == "" {
		host = "localhost"
	}
	port := ds.Port
	if port == 0 {
		port = 3306
	}
	timeout := ds.ConnectTimeout
	if timeout == 0 {
		timeout = 10
	}
	params := url.Values{}
	params.Set("timeout", fmt.Sprintf("%ds", timeout))
	charset := ds.Charset
	if charset == "" {
		charset = "utf8mb4"
	}
	params.Set("charset", charset)
	if ds.SQLMode != "" {
		params.Set("sql_mode", ds.SQLMode)
	}
	if ds.Collation != "" {
		params.Set("collation", ds.Collation)
	}
	if ds.SSLMode != "" {
		params.Set("tls", ds.SSLMode)
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?%s",
		ds.User, ds.Password, host, port, ds.Database, params.Encode())
}

// Pool wraps a *sql.DB for the active datasource.
type Pool struct {
	DB *sql.DB
}

// Open opens a pooled connection and verifies it with a Ping.
func Open(ctx context.Context, ds config.Datasource) (*Pool, error) {
	db, err := sql.Open("mysql", DSN(ds))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	pingTimeout := ds.ConnectTimeout
	if pingTimeout <= 0 {
		pingTimeout = 10
	}
	pingCtx, cancel := context.WithTimeout(ctx, time.Duration(pingTimeout)*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, err
	}
	return &Pool{DB: db}, nil
}

// Ping verifies the connection is alive.
func (p *Pool) Ping(ctx context.Context) error {
	return p.DB.PingContext(ctx)
}

// Close releases the pool.
func (p *Pool) Close() error {
	return p.DB.Close()
}
