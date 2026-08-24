// Package watch reloads files when they change on disk, so a running server
// picks up schedule/holiday edits without a restart.
//
// It polls modification times rather than using fsnotify. Polling is a
// deliberate choice, not a shortcut: os.Stat follows symlinks, so it also
// catches Kubernetes ConfigMap updates, which swap the mount's ..data symlink
// atomically -- an inode-level watch would keep watching the old file and miss
// them. It also adds no dependencies and naturally coalesces rapid edits.
package watch

import (
	"context"
	"os"
	"time"
)

// target is one watched file and what to do when it changes.
type target struct {
	path    string
	reload  func() error
	lastMod time.Time
	present bool // whether the file existed at the last poll
}

// Watcher polls a set of files and runs each file's reload callback when its
// modification time changes. A zero-value Watcher is not usable; call New.
type Watcher struct {
	interval time.Duration
	logf     func(format string, args ...any)
	targets  []*target
}

// New returns a Watcher that polls every interval and reports reloads/errors
// through logf (e.g. log.Printf). interval must be > 0.
func New(interval time.Duration, logf func(string, ...any)) *Watcher {
	return &Watcher{interval: interval, logf: logf}
}

// Add registers path with a reload callback, invoked whenever path's modtime
// changes. The current modtime is recorded now, so only changes after Add
// trigger a reload -- the freshly loaded state at startup is not reloaded
// redundantly.
func (w *Watcher) Add(path string, reload func() error) {
	t := &target{path: path, reload: reload}
	if fi, err := os.Stat(path); err == nil {
		t.lastMod, t.present = fi.ModTime(), true
	}
	w.targets = append(w.targets, t)
}

// Run polls until ctx is cancelled. It blocks, so run it in its own goroutine.
func (w *Watcher) Run(ctx context.Context) {
	tick := time.NewTicker(w.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, t := range w.targets {
				w.poll(t)
			}
		}
	}
}

// poll reloads t if its file changed since the last observation. A file that
// briefly disappears (mid-rename, or during a ConfigMap swap) is treated as
// "not present" and reloaded once it reappears.
func (w *Watcher) poll(t *target) {
	fi, err := os.Stat(t.path)
	if err != nil {
		t.present = false
		return
	}
	mod := fi.ModTime()
	if t.present && mod.Equal(t.lastMod) {
		return
	}
	t.lastMod, t.present = mod, true
	if err := t.reload(); err != nil {
		w.logf("watch: reload %s failed, keeping previous copy: %v", t.path, err)
		return
	}
	w.logf("watch: reloaded %s", t.path)
}
