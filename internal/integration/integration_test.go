//go:build integration

package integration

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/cli"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
)

var dsn string

func TestMain(m *testing.M) {
	if os.Getenv("RUN_INTEGRATION") == "" {
		os.Exit(m.Run()) // skip container setup when flag absent
	}
	ctx := context.Background()
	// RunContainer uses the default mysql:8 image and waits for readiness.
	c, err := tcmysql.RunContainer(ctx,
		tcmysql.WithUsername("root"),
		tcmysql.WithPassword("test"),
		tcmysql.WithDatabase("test"),
	)
	if err != nil {
		panic(err)
	}
	defer c.Terminate(ctx)
	dsn, _ = c.ConnectionString(ctx, "charset=utf8mb4")
	os.Exit(m.Run())
}

func parseConn(dsn string) (host, port, user, pass, db string) {
	// root:test@tcp(127.0.0.1:32768)/test?...
	rest := dsn
	if i := strings.Index(rest, "@tcp("); i >= 0 {
		up := rest[:i]
		if j := strings.Index(up, ":"); j >= 0 {
			user = up[:j]
			pass = up[j+1:]
		}
		rest = rest[i+5:]
	}
	if i := strings.Index(rest, ")"); i >= 0 {
		hp := rest[:i]
		if j := strings.LastIndex(hp, ":"); j >= 0 {
			host = hp[:j]
			port = hp[j+1:]
		}
		rest = rest[i+1:]
	}
	rest = strings.TrimPrefix(rest, "/")
	if i := strings.Index(rest, "?"); i >= 0 {
		db = rest[:i]
	} else {
		db = rest
	}
	return
}

func run(t *testing.T, args ...string) int {
	host, port, user, pass, db := parseConn(dsn)
	all := append([]string{}, args...)
	all = append(all, "--host", host, "--port", port, "--user", user, "--password", pass, "--db", db, "--format", "json")
	return cli.Run(all)
}

func TestQuerySelect(t *testing.T) {
	if dsn == "" {
		t.Skip("integration not enabled")
	}
	code := run(t, "query", "SELECT 1 AS one")
	if code != cli.ExitOK {
		t.Fatalf("expected OK, got %d", code)
	}
}

func TestQueryReadonlyBlocksWrite(t *testing.T) {
	if dsn == "" {
		t.Skip("integration not enabled")
	}
	code := run(t, "query", "CREATE TABLE t (id int)")
	if code != cli.ExitDDLRequiresWrite && code != cli.ExitReadonlyViolation {
		t.Fatalf("expected guard exit, got %d", code)
	}
}

func TestWriteThenQuery(t *testing.T) {
	if dsn == "" {
		t.Skip("integration not enabled")
	}
	if code := run(t, "query", "CREATE TABLE t (id int)", "--write", "--ddl", "--yes"); code != cli.ExitOK {
		t.Fatalf("create failed: %d", code)
	}
	if code := run(t, "query", "INSERT INTO t VALUES (1)", "--write"); code != cli.ExitOK {
		t.Fatalf("insert failed: %d", code)
	}
	if code := run(t, "txn", "INSERT INTO t VALUES (2)", "INSERT INTO t VALUES (3)", "--write", "--yes"); code != cli.ExitOK {
		t.Fatalf("txn failed: %d", code)
	}
	code := run(t, "query", "SELECT COUNT(*) AS c FROM t")
	if code != cli.ExitOK {
		t.Fatalf("count failed: %d", code)
	}
}

func TestSchemaAndSample(t *testing.T) {
	if dsn == "" {
		t.Skip("integration not enabled")
	}
	run(t, "query", "CREATE TABLE t (id int, name varchar(20))", "--write", "--ddl", "--yes")
	run(t, "query", "INSERT INTO t VALUES (1,'a')", "--write")
	if code := run(t, "schema", "t"); code != cli.ExitOK {
		t.Fatalf("schema: %d", code)
	}
	if code := run(t, "sample", "t", "-n", "5"); code != cli.ExitOK {
		t.Fatalf("sample: %d", code)
	}
	if code := run(t, "tables"); code != cli.ExitOK {
		t.Fatalf("tables: %d", code)
	}
	if code := run(t, "databases"); code != cli.ExitOK {
		t.Fatalf("databases: %d", code)
	}
}
