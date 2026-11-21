// Package dbtest opens a fresh, migrated queue database for one test, on
// each engine the run can reach. Only tests import it.
//
// The migrations are read from the module's own migrations/ directory on
// disk. The embed that ships them is in the main package, where the core's
// gate admits it (ADR-20261021-bqc rule 33), and no other package can import that.
//
// SQLite is a file in the test's own directory. Postgres is a new database on
// the server AUTHZ_TEST_POSTGRES_DSN names, which `make test-engines` starts,
// and is dropped afterwards. A run without it skips Postgres and says so.
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lib/pq"
	_ "modernc.org/sqlite" // the SQLite driver, which sdk/moduledb leaves to the module

	"github.com/prusa3d/prusa-connect/sdk/moduledb"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// The two variables the core's dual-engine fixture sets.
const (
	postgresDSNEnv     = "AUTHZ_TEST_POSTGRES_DSN"
	requiredEnginesEnv = "AUTHZ_REQUIRED_ENGINES"
)

// Engine makes a fresh, empty database of one engine.
type Engine struct {
	Name  wirerpc.DatabaseEngine
	Grant func(tb testing.TB) wirerpc.DatabaseGrant
}

// Engines answers SQLite, and Postgres when the run names a server.
func Engines(tb testing.TB) []Engine {
	tb.Helper()

	out := []Engine{{Name: wirerpc.DatabaseSQLite, Grant: sqliteGrant}}

	dsn := os.Getenv(postgresDSNEnv)
	if dsn == "" {
		if strings.Contains(os.Getenv(requiredEnginesEnv), "postgres") {
			tb.Fatalf("%s names postgres and %s is empty", requiredEnginesEnv, postgresDSNEnv)
		}

		tb.Logf("postgres skipped: %s is empty", postgresDSNEnv)

		return out
	}

	return append(out, Engine{Name: wirerpc.DatabasePostgres, Grant: func(tb testing.TB) wirerpc.DatabaseGrant {
		tb.Helper()

		return postgresGrant(tb, dsn)
	}})
}

// Schema is the module's migrations as the main package embeds them, read
// from disk.
func Schema() moduledb.Schema {
	_, here, _, _ := runtime.Caller(0)

	return moduledb.Schema{Migrations: os.DirFS(filepath.Join(filepath.Dir(here), "..", ".."))}
}

// Open opens a database the grant names and migrates it. The handle is
// closed when the test ends.
func Open(tb testing.TB, grant wirerpc.DatabaseGrant) *sql.DB {
	tb.Helper()

	db, _, err := moduledb.Open(context.Background(), grant, Schema())
	if err != nil {
		tb.Fatalf("open %s: %v", grant, err)
	}

	tb.Cleanup(func() { _ = db.Close() })

	return db
}

// SQLite is a fresh, migrated SQLite database, which most cases run on.
func SQLite(tb testing.TB) (*sql.DB, wirerpc.DatabaseGrant) {
	tb.Helper()

	grant := sqliteGrant(tb)

	return Open(tb, grant), grant
}

func sqliteGrant(tb testing.TB) wirerpc.DatabaseGrant {
	tb.Helper()

	return wirerpc.DatabaseGrant{Engine: wirerpc.DatabaseSQLite, DSN: filepath.Join(tb.TempDir(), "database.sqlite")}
}

// postgresGrant creates a database of the case's own and drops it afterwards.
func postgresGrant(tb testing.TB, dsn string) wirerpc.DatabaseGrant {
	tb.Helper()

	server, err := url.Parse(dsn)
	if err != nil || server.Scheme == "" {
		tb.Fatalf("%s must be a postgres:// URL", postgresDSNEnv)
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		tb.Fatalf("reach the postgres server: %v", err)
	}

	name := "queuetest_" + strings.ToLower(rand.Text())[:12]

	// The name is made here and quoted. CREATE DATABASE takes no parameter.
	if _, err = admin.ExecContext(context.Background(), "CREATE DATABASE "+pq.QuoteIdentifier(name)); err != nil {
		tb.Fatalf("create database %s: %v", name, err)
	}

	tb.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(),
			"DROP DATABASE IF EXISTS "+pq.QuoteIdentifier(name)+" WITH (FORCE)")
		_ = admin.Close()
	})

	server.Path = "/" + name

	return wirerpc.DatabaseGrant{Engine: wirerpc.DatabasePostgres, DSN: server.String()}
}
