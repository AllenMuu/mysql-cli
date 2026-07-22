package conn

import (
	"context"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/config"
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

func TestDSNDefaults(t *testing.T) {
	ds := config.Datasource{User: "u", Password: "p"}
	dsn := DSN(ds)
	assert.Contains(t, dsn, "tcp(localhost:3306)")
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
