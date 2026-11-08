package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestWaitDatabaseClosedToleratesDrainButNotARealHolder(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("open-file inspection is Linux only")
	}
	path := filepath.Join(t.TempDir(), "live.db")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := EnsureDatabaseClosed(path); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("an open database must be refused immediately: %v", err)
	}
	// A handle released shortly after, as an in-flight query finishing is.
	go func() { time.Sleep(150 * time.Millisecond); file.Close() }()
	if err := WaitDatabaseClosed(context.Background(), path, 5*time.Second); err != nil {
		t.Fatalf("draining handle was treated as a second user: %v", err)
	}
	// A holder that never lets go is still refused at the deadline.
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := WaitDatabaseClosed(context.Background(), path, 100*time.Millisecond); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("persistent holder not refused: %v", err)
	}
}
