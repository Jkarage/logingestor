// Package dbtest creates throwaway, fully migrated Postgres databases for
// integration tests.
//
// The unit tests in this repo cover pure logic. Three of the bugs that actually
// reached production could not have been caught that way: the retention rollup
// repair was consistent in Go and wrong against real rows, the org-wide log
// query passed build, vet and every unit test while returning nothing, and a
// dedup upsert is only meaningful against a real partial unique index. Those
// need a database.
//
// Tests using this package skip unless a server is configured, so the default
// `go test ./...` stays hermetic:
//
//	TEST_DB_HOST=localhost:5432 TEST_DB_USER=postgres TEST_DB_PASSWORD=postgres \
//	TEST_DB_DISABLE_TLS=true go test ./... -run Integration
//
// Each call to New creates a new database named logingestor_test_<random>, runs
// the migrations into it, and drops it when the test ends. No pre-existing
// database is ever read or written, so pointing this at a shared server is
// safe — though a container is still the better habit.
package dbtest

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/sdk/migrate"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Environment variables describing the server to create test databases on.
const (
	EnvHost       = "TEST_DB_HOST"
	EnvUser       = "TEST_DB_USER"
	EnvPassword   = "TEST_DB_PASSWORD"
	EnvDisableTLS = "TEST_DB_DISABLE_TLS"

	// EnvAdminName is the existing database to connect to in order to issue
	// CREATE DATABASE. It is never modified.
	EnvAdminName = "TEST_DB_ADMIN_NAME"
)

// namePattern is the only shape of database name this package will create or
// drop. Every drop is checked against it, so a bug in name construction fails
// the test instead of dropping something real.
var namePattern = regexp.MustCompile(`^logingestor_test_[0-9a-f]{12}$`)

// server is the configured target, or ok=false when the environment says to
// skip integration tests.
type server struct {
	host       string
	user       string
	password   string
	adminName  string
	disableTLS bool
}

func serverFromEnv() (server, bool) {
	host := os.Getenv(EnvHost)
	if host == "" {
		return server{}, false
	}

	s := server{
		host:      host,
		user:      os.Getenv(EnvUser),
		password:  os.Getenv(EnvPassword),
		adminName: os.Getenv(EnvAdminName),
	}
	if s.adminName == "" {
		s.adminName = "postgres"
	}

	// Default to disabled TLS: the common target is a local container that has
	// none. Anything other than an explicit false-ish value keeps it disabled.
	switch strings.ToLower(os.Getenv(EnvDisableTLS)) {
	case "false", "0", "no":
		s.disableTLS = false
	default:
		s.disableTLS = true
	}

	return s, true
}

func (s server) config(name string) sqldb.Config {
	return sqldb.Config{
		User:         s.user,
		Password:     s.password,
		Host:         s.host,
		Name:         name,
		MaxIdleConns: 2,
		MaxOpenConns: 4,
		DisableTLS:   s.disableTLS,
	}
}

// Database is a migrated, private database bound to the lifetime of one test.
type Database struct {
	DB   *sqlx.DB
	Log  *logger.Logger
	Name string
}

// New creates and migrates a throwaway database. The test is skipped when no
// server is configured, and the database is dropped during cleanup.
func New(t *testing.T) *Database {
	t.Helper()

	srv, ok := serverFromEnv()
	if !ok {
		t.Skipf("integration test skipped: set %s (and %s/%s) to run it", EnvHost, EnvUser, EnvPassword)
	}

	admin, err := sqldb.Open(srv.config(srv.adminName))
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := sqldb.StatusCheck(ctx, admin); err != nil {
		t.Fatalf("admin status check: %v", err)
	}

	name := "logingestor_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if !namePattern.MatchString(name) {
		t.Fatalf("refusing to create database with unexpected name %q", name)
	}

	if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", name)); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}

	db, err := sqldb.Open(srv.config(name))
	if err != nil {
		dropDatabase(t, srv, name)
		t.Fatalf("open %s: %v", name, err)
	}

	// Migrations are the thing under test as much as the queries are: a schema
	// built any other way would not catch a migration that fails on a fresh
	// database.
	if err := migrate.Migrate(ctx, db); err != nil {
		db.Close()
		dropDatabase(t, srv, name)
		t.Fatalf("migrate %s: %v", name, err)
	}

	t.Cleanup(func() {
		db.Close()
		dropDatabase(t, srv, name)
	})

	return &Database{
		DB:   db,
		Log:  logger.New(testWriter{t}, logger.LevelInfo, "test", func(context.Context) string { return "" }),
		Name: name,
	}
}

// dropDatabase removes a database this package created. Connections are
// terminated first so a leaked pool connection cannot block the drop and leave
// databases behind on the server.
func dropDatabase(t *testing.T, srv server, name string) {
	t.Helper()

	if !namePattern.MatchString(name) {
		t.Fatalf("refusing to drop database with unexpected name %q", name)
	}

	admin, err := sqldb.Open(srv.config(srv.adminName))
	if err != nil {
		t.Logf("cleanup: open admin database: %v", err)
		return
	}
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const terminate = `
	SELECT pg_terminate_backend(pid)
	FROM pg_stat_activity
	WHERE datname = $1 AND pid <> pg_backend_pid()`

	if _, err := admin.ExecContext(ctx, terminate, name); err != nil {
		t.Logf("cleanup: terminate connections to %s: %v", name, err)
	}

	if _, err := admin.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", name)); err != nil {
		t.Logf("cleanup: drop database %s: %v", name, err)
	}
}

// testWriter routes service logs into the test's output, where they only appear
// if the test fails or -v is set.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", strings.TrimSpace(string(p)))
	return len(p), nil
}
