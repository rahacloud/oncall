package store

import (
	"os"
	"path/filepath"
	"testing"
)

const schedV1 = `people:
  ali: {name: Ali}
shifts:
  - {start: "1405-01-01", end: "1405-01-07", person: ali}
`

const schedV2 = `people:
  sara: {name: Sara}
shifts:
  - {start: "1405-01-01", end: "1405-01-07", person: sara}
`

func TestReload_PicksUpExternalEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.yaml")
	if err := os.WriteFile(path, []byte(schedV1), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Snapshot().Shifts[0].Person; got != "ali" {
		t.Fatalf("initial person = %q, want ali", got)
	}

	// Simulate an external edit (GitOps sync / ConfigMap update).
	if err := os.WriteFile(path, []byte(schedV2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := st.Snapshot().Shifts[0].Person; got != "sara" {
		t.Fatalf("after reload person = %q, want sara", got)
	}
}

func TestReload_KeepsPreviousCopyOnParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.yaml")
	if err := os.WriteFile(path, []byte(schedV1), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// A broken save must not take down the served schedule.
	if err := os.WriteFile(path, []byte("people: [not: a: map"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Reload(); err == nil {
		t.Fatal("expected a parse error on reload")
	}
	if got := st.Snapshot().Shifts[0].Person; got != "ali" {
		t.Fatalf("previous copy lost after bad reload: person = %q, want ali", got)
	}
}
