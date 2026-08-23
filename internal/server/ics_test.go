package server

import (
	"strings"
	"testing"
	"time"

	"github.com/rahacloud/oncall/internal/report"
)

func day(y int, m time.Month, d int, person, source, shift, note string) report.Day {
	return report.Day{
		G: time.Date(y, m, d, 0, 0, 0, 0, time.UTC),
		Person: person, Source: source, Shift: shift, Note: note,
	}
}

func TestBuildICS_CoalescesAndExclusiveEnd(t *testing.T) {
	version := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	days := []report.Day{
		day(2026, 6, 6, "Ali Karimi", "schedule", "primary", ""),
		day(2026, 6, 7, "Ali Karimi", "schedule", "primary", ""),
		day(2026, 6, 8, "Ali Karimi", "schedule", "primary", ""),
		day(2026, 6, 9, "Sara Ahmadi", "override", "", "swap: mehdi away"),
	}
	out := string(buildICS(days, version))

	if n := strings.Count(out, "BEGIN:VEVENT"); n != 2 {
		t.Fatalf("want 2 coalesced events, got %d\n%s", n, out)
	}
	// Three contiguous days -> one all-day event; DTEND is exclusive (day+1).
	if !strings.Contains(out, "DTSTART;VALUE=DATE:20260606") ||
		!strings.Contains(out, "DTEND;VALUE=DATE:20260609") {
		t.Errorf("coalesced run has wrong bounds:\n%s", out)
	}
	// Override must be reflected, carrying its note as the description.
	if !strings.Contains(out, "DESCRIPTION:swap: mehdi away") {
		t.Errorf("override not reflected in feed:\n%s", out)
	}
	// Every VEVENT needs DTSTAMP (RFC 5545 §3.6.1) and the version-seeded stamp.
	if strings.Count(out, "DTSTAMP:20260102T030405Z") != 2 {
		t.Errorf("expected a version-seeded DTSTAMP per event:\n%s", out)
	}
}

func TestBuildICS_FoldsLongLines(t *testing.T) {
	// A Persian name well over 75 octets (2 bytes/char in UTF-8) forces folding.
	long := strings.Repeat("محمدرضا", 12)
	out := string(buildICS([]report.Day{day(2026, 6, 6, long, "schedule", "", "")}, time.Now()))

	for _, line := range strings.Split(out, "\r\n") {
		if len(line) > 75 {
			t.Fatalf("line exceeds 75 octets (%d): %q", len(line), line)
		}
	}
	// Folded continuation lines must begin with a single space per RFC 5545 §3.1.
	if !strings.Contains(out, "\r\n ") {
		t.Errorf("expected a folded continuation line for a long summary")
	}
}

func TestUID_StableRegardlessOfPosition(t *testing.T) {
	r := run{start: time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC), person: "Ali Karimi", source: "schedule"}
	if got := uid(r); got != uid(r) {
		t.Fatal("uid is not deterministic")
	}
	// A different person at the same start yields a different, stable UID.
	other := r
	other.person = "Sara Ahmadi"
	if uid(r) == uid(other) {
		t.Error("distinct assignments collided on the same UID")
	}
}
