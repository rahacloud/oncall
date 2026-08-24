// Package store wraps a schedule with a read/write lock and write-through
// persistence, so the HTTP server can serve and mutate it concurrently.
package store

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rahacloud/oncall/internal/schedule"
)

// Store guards a schedule and persists every mutation back to its YAML file.
type Store struct {
	mu        sync.RWMutex
	path      string
	sched     *schedule.Schedule
	updatedAt time.Time // bumped on every mutation; seeds the ICS feed's DTSTAMP/ETag
}

// Open loads the schedule at path.
func Open(path string) (*Store, error) {
	s, err := schedule.Load(path)
	if err != nil {
		return nil, err
	}
	updated := time.Now()
	if fi, err := os.Stat(path); err == nil {
		updated = fi.ModTime()
	}
	return &Store{path: path, sched: s, updatedAt: updated}, nil
}

// Snapshot returns a deep copy safe to read without holding the lock.
func (s *Store) Snapshot() *schedule.Schedule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sched.Clone()
}

// SnapshotWithVersion returns a deep copy together with the data version (last
// mutation time), read atomically. The version drives the ICS feed's DTSTAMP,
// LAST-MODIFIED, SEQUENCE, and ETag so the body is byte-stable between changes.
func (s *Store) SnapshotWithVersion() (*schedule.Schedule, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sched.Clone(), s.updatedAt
}

// mutate applies fn under the write lock, persists, and keeps the new state only
// if the write succeeds.
func (s *Store) mutate(fn func(*schedule.Schedule)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.sched.Clone()
	fn(next)
	if err := next.Save(s.path); err != nil {
		return err
	}
	s.sched = next
	s.updatedAt = time.Now()
	return nil
}

// AddShift appends a shift.
func (s *Store) AddShift(sh schedule.Shift) error {
	return s.mutate(func(sc *schedule.Schedule) { sc.Shifts = append(sc.Shifts, sh) })
}

// AddOverride appends an override (the common "swap" operation).
func (s *Store) AddOverride(o schedule.Override) error {
	return s.mutate(func(sc *schedule.Schedule) { sc.Overrides = append(sc.Overrides, o) })
}

// DeleteOverride removes the override at index.
func (s *Store) DeleteOverride(index int) error {
	s.mu.RLock()
	n := len(s.sched.Overrides)
	s.mu.RUnlock()
	if index < 0 || index >= n {
		return fmt.Errorf("override index %d out of range (0..%d)", index, n-1)
	}
	return s.mutate(func(sc *schedule.Schedule) {
		sc.Overrides = append(sc.Overrides[:index], sc.Overrides[index+1:]...)
	})
}

// UpsertPerson adds or updates a person.
func (s *Store) UpsertPerson(id string, p schedule.Person) error {
	return s.mutate(func(sc *schedule.Schedule) {
		if sc.People == nil {
			sc.People = map[string]schedule.Person{}
		}
		sc.People[id] = p
	})
}

// Replace swaps in a whole new schedule.
func (s *Store) Replace(ns *schedule.Schedule) error {
	return s.mutate(func(sc *schedule.Schedule) {
		*sc = *ns.Clone()
	})
}
