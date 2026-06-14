package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

func TestSessionStoreRoundTrip(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, ":memory:")
	store := database.NewSessionStore(db)

	if err := store.Commit("tok", []byte("data"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, found, err := store.Find("tok")
	if err != nil || !found || string(got) != "data" {
		t.Fatalf("Find = (%q,%v,%v), want (data,true,nil)", got, found, err)
	}

	// Overwrite (upsert) updates the data.
	if err := store.Commit("tok", []byte("data2"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Commit overwrite: %v", err)
	}
	got, _, _ = store.Find("tok")
	if string(got) != "data2" {
		t.Errorf("Find after overwrite = %q, want data2", got)
	}

	if err := store.Delete("tok"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := store.Find("tok"); found {
		t.Error("Find after Delete returned found=true")
	}
}

func TestSessionStoreExpiry(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, ":memory:")
	store := database.NewSessionStore(db)

	// An already-expired session is not found...
	if err := store.Commit("old", []byte("x"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, found, _ := store.Find("old"); found {
		t.Error("expired session was found")
	}

	// ...and DeleteExpired reaps it while keeping a live one.
	if err := store.Commit("live", []byte("y"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Commit live: %v", err)
	}
	if err := store.DeleteExpired(context.Background()); err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if _, found, _ := store.Find("live"); !found {
		t.Error("live session removed by DeleteExpired")
	}
	var n int
	if err := db.QueryRowContext(context.Background(),
		"SELECT count(*) FROM sessions").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("sessions after DeleteExpired = %d, want 1", n)
	}
}
