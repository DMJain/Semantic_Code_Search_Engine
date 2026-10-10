// Package testdb gives Postgres integration tests a private throwaway
// database, so tests never touch the dev database named in
// TEST_DATABASE_URL and can run in parallel across packages.
//
// With TEST_DATABASE_URL empty a test is skipped, unless REQUIRE_TEST_DB=1
// (set by `make test`, `make ci-local` and CI), in which case it fails.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

const (
	// Prefix starts the name of every database this package creates; it
	// refuses to drop anything else. `make test-db-clean` matches on it.
	Prefix = "codesearch_test_"

	// maxNameBytes is Postgres's identifier limit; longer names are
	// silently truncated, which would lose the random suffix.
	maxNameBytes = 63
)

// New returns a pool on a fresh, fully migrated throwaway database that is
// dropped when the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dbURL := Create(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("testdb: migrate throwaway database: %v", err)
	}
	pool, err := store.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("testdb: open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Create returns the URL of a fresh, empty (unmigrated) throwaway database
// that is dropped when the test ends. Use it for tests that drive migrations
// themselves.
func Create(t testing.TB) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		if os.Getenv("REQUIRE_TEST_DB") == "1" {
			t.Fatal("testdb: TEST_DATABASE_URL is not set but REQUIRE_TEST_DB=1")
		}
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, err := withDatabase(base, "postgres")
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("testdb: connect to maintenance database: %v", err)
	}

	name := Name(binaryName(), randomHex(t))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("testdb: create %s: %v", name, err)
	}
	t.Cleanup(func() {
		defer func() { _ = admin.Close(context.Background()) }()
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := drop(dropCtx, admin, name); err != nil {
			t.Errorf("testdb: %v", err)
		}
	})

	dbURL, err := withDatabase(base, name)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	return dbURL
}

// Name builds a throwaway database name: Prefix, the package name lowercased
// with anything outside [a-z0-9_] replaced by '_', and the suffix. The
// package part is shortened so the whole name fits in maxNameBytes.
func Name(pkg, suffix string) string {
	pkg = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return '_'
	}, pkg)
	room := maxNameBytes - len(Prefix) - len("_") - len(suffix)
	if len(pkg) > room {
		pkg = pkg[:max(room, 0)]
	}
	return Prefix + pkg + "_" + suffix
}

// drop removes a throwaway database. It refuses any name without Prefix, so a
// bug can never drop the dev database.
func drop(ctx context.Context, admin *pgx.Conn, name string) error {
	if !strings.HasPrefix(name, Prefix) {
		return fmt.Errorf("refusing to drop %q: name lacks the %s prefix", name, Prefix)
	}
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop %s: %w", name, err)
	}
	return nil
}

func withDatabase(raw, dbName string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse TEST_DATABASE_URL: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// binaryName is the test binary's name without ".test", which is the
// package name under `go test`.
func binaryName() string {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".test")
	if name == "" {
		return "pkg"
	}
	return name
}

func randomHex(t testing.TB) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("testdb: random suffix: %v", err)
	}
	return hex.EncodeToString(b)
}
