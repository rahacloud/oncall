package watch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// bump writes content and forces a strictly later modtime, so the change is
// observable even on filesystems with coarse timestamp resolution.
func bump(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

func TestPoll_ReloadsOnChangeNotOnStillness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.yaml")
	bump(t, path, "v1")

	w := New(time.Millisecond, func(string, ...any) {})
	var reloads int
	w.Add(path, func() error { reloads++; return nil })

	// Unchanged file: polling must not reload.
	w.poll(w.targets[0])
	if reloads != 0 {
		t.Fatalf("reloaded an unchanged file: %d", reloads)
	}

	// Changed file: exactly one reload.
	bump(t, path, "v2")
	w.poll(w.targets[0])
	if reloads != 1 {
		t.Fatalf("want 1 reload after change, got %d", reloads)
	}

	// Still unchanged again: no further reloads.
	w.poll(w.targets[0])
	if reloads != 1 {
		t.Fatalf("reloaded without a change: %d", reloads)
	}
}

func TestPoll_KeepsWatchingAfterReloadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.yaml")
	bump(t, path, "v1")

	w := New(time.Millisecond, func(string, ...any) {})
	var reloads int
	w.Add(path, func() error {
		reloads++
		return errors.New("boom") // e.g. an invalid save mid-edit
	})

	bump(t, path, "bad")
	w.poll(w.targets[0])
	// A failing reload must not wedge the watcher: a later change still fires.
	bump(t, path, "good")
	w.poll(w.targets[0])
	if reloads != 2 {
		t.Fatalf("want 2 reload attempts across errors, got %d", reloads)
	}
}

func TestPoll_ReloadsWhenFileReappears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.yaml")
	bump(t, path, "v1")

	w := New(time.Millisecond, func(string, ...any) {})
	var reloads int
	w.Add(path, func() error { reloads++; return nil })

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w.poll(w.targets[0]) // absent: no reload, but noted as not-present
	if reloads != 0 {
		t.Fatalf("reloaded a missing file: %d", reloads)
	}
	bump(t, path, "v2")
	w.poll(w.targets[0]) // reappeared: reload
	if reloads != 1 {
		t.Fatalf("want reload after file reappears, got %d", reloads)
	}
}
