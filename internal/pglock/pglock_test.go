package pglock

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// open returns a pool against TEST_DATABASE_URL, skipping the test when it is
// unset — advisory locks need no schema at all, so this needs no migration
// and no app database, just a real Postgres to hold the lock in.
func open(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL (a throwaway database) to run the pglock tests")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestTryLockExcludesASecondHolder(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	unlock, ok, err := TryLock(ctx, db, "pglock-test-exclude")
	if err != nil || !ok {
		t.Fatalf("first TryLock: ok=%v err=%v", ok, err)
	}

	_, ok2, err := TryLock(ctx, db, "pglock-test-exclude")
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Fatal("a second holder acquired a lock the first still holds")
	}

	unlock()

	unlock3, ok3, err := TryLock(ctx, db, "pglock-test-exclude")
	if err != nil || !ok3 {
		t.Fatalf("after unlock, TryLock: ok=%v err=%v", ok3, err)
	}
	unlock3()
}

func TestTryLockDoesNotExcludeADifferentName(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	unlock, ok, err := TryLock(ctx, db, "pglock-test-a")
	if err != nil || !ok {
		t.Fatalf("TryLock a: ok=%v err=%v", ok, err)
	}
	defer unlock()

	unlockB, okB, err := TryLock(ctx, db, "pglock-test-b")
	if err != nil || !okB {
		t.Fatalf("TryLock b: ok=%v err=%v", okB, err)
	}
	unlockB()
}

func TestWithLockSkipsWhenAlreadyHeld(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	unlock, ok, err := TryLock(ctx, db, "pglock-test-withlock")
	if err != nil || !ok {
		t.Fatalf("TryLock: ok=%v err=%v", ok, err)
	}
	defer unlock()

	ran, err := WithLock(ctx, db, "pglock-test-withlock", func() {
		t.Fatal("fn ran while the lock was already held elsewhere")
	})
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("WithLock reported it ran despite the lock being held")
	}
}

func TestWithLockRunsAndReleases(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	called := false
	ran, err := WithLock(ctx, db, "pglock-test-run", func() { called = true })
	if err != nil || !ran || !called {
		t.Fatalf("ran=%v called=%v err=%v", ran, called, err)
	}

	// The lock must be free again immediately after WithLock returns.
	unlock, ok, err := TryLock(ctx, db, "pglock-test-run")
	if err != nil || !ok {
		t.Fatalf("lock still held after WithLock returned: ok=%v err=%v", ok, err)
	}
	unlock()
}

func TestUnlockIsIdempotent(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	unlock, ok, err := TryLock(ctx, db, "pglock-test-idempotent")
	if err != nil || !ok {
		t.Fatalf("TryLock: ok=%v err=%v", ok, err)
	}
	unlock()
	unlock() // must not panic or hang

	// Give the connection a moment to actually close before reusing the name.
	time.Sleep(10 * time.Millisecond)
	unlock2, ok2, err := TryLock(ctx, db, "pglock-test-idempotent")
	if err != nil || !ok2 {
		t.Fatalf("lock unavailable after idempotent unlock: ok=%v err=%v", ok2, err)
	}
	unlock2()
}
