package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rahacloud/oncall/internal/holiday"
	"github.com/rahacloud/oncall/internal/jalali"
	"github.com/rahacloud/oncall/internal/report"
	"github.com/rahacloud/oncall/internal/schedule"
	"github.com/rahacloud/oncall/internal/store"
)

func day(y int, m time.Month, d int, person, source, shift, note string) report.Day {
	return report.Day{
		G:      time.Date(y, m, d, 0, 0, 0, 0, time.UTC),
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
	out := string(buildICS(days, version, "On-Call"))

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
	out := string(buildICS([]report.Day{day(2026, 6, 6, long, "schedule", "", "")}, time.Now(), "On-Call"))

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

func TestBuildICS_CalNameInHeader(t *testing.T) {
	out := string(buildICS(nil, time.Now(), "On-Call — Ali Karimi"))
	if !strings.Contains(out, "X-WR-CALNAME:On-Call — Ali Karimi") {
		t.Errorf("calendar name not in header:\n%s", out)
	}
}

func TestKnownPersonIDs_UnionAndCaseFold(t *testing.T) {
	sch := &schedule.Schedule{
		People:    map[string]schedule.Person{"ali.karimi": {Name: "Ali"}},
		Shifts:    []schedule.Shift{{Person: "reza.hosseini"}}, // shift-only, no people entry
		Overrides: []schedule.Override{{Person: "Sara.Ahmadi"}},
	}
	ids := knownPersonIDs(sch)
	// Keys are lowercased; values are the canonical (original-case) ids.
	for lower, want := range map[string]string{
		"ali.karimi":    "ali.karimi",
		"reza.hosseini": "reza.hosseini",
		"sara.ahmadi":   "Sara.Ahmadi",
	} {
		if got, ok := ids[lower]; !ok || got != want {
			t.Errorf("ids[%q] = %q,%v; want %q,true", lower, got, ok, want)
		}
	}
}

func TestFilterByPersonID_CaseInsensitive(t *testing.T) {
	days := []report.Day{
		{PersonID: "ali.karimi", Person: "Ali"},
		{PersonID: "sara.ahmadi", Person: "Sara"},
		{PersonID: "", Person: "(gap)"},
	}
	got := filterByPersonID(days, "ALI.KARIMI")
	if len(got) != 1 || got[0].PersonID != "ali.karimi" {
		t.Fatalf("filter = %+v; want only ali.karimi", got)
	}
}

// newTestServer builds a Server over a temp schedule whose two shifts sit inside
// the feed window (relative to today), so filtering has real events to match.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	now := time.Now()
	j := func(off int) string { return jalali.FromTime(now.AddDate(0, 0, off)).String() }
	yaml := fmt.Sprintf(`people:
  ali.karimi: {name: Ali Karimi}
  sara.ahmadi: {name: Sara Ahmadi}
  mahdi.idle: {name: Mahdi Idle}
shifts:
  - {start: "%s", end: "%s", person: ali.karimi}
  - {start: "%s", end: "%s", person: sara.ahmadi}
`, j(0), j(5), j(6), j(10))

	path := filepath.Join(t.TempDir(), "schedule.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	hol, _ := holiday.Load("")
	return New(st, hol, "")
}

func getICS(t *testing.T, srv *Server, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/calendar.ics"+query, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestHandleICS_UserFilter(t *testing.T) {
	srv := newTestServer(t)

	// Unfiltered: both people appear.
	all := getICS(t, srv, "").Body.String()
	if !strings.Contains(all, "On-call: Ali Karimi") || !strings.Contains(all, "On-call: Sara Ahmadi") {
		t.Fatalf("everyone feed missing a person:\n%s", all)
	}

	// Filtered by id (case-insensitive): only that person, labelled, named file.
	rec := getICS(t, srv, "?user=ALI.KARIMI")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(body, "On-call: Ali Karimi") || strings.Contains(body, "Sara Ahmadi") {
		t.Errorf("filtered feed should contain only Ali:\n%s", body)
	}
	if !strings.Contains(body, "X-WR-CALNAME:On-Call — Ali Karimi") {
		t.Errorf("missing per-person calendar name:\n%s", body)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="oncall-ali.karimi.ics"`) {
		t.Errorf("Content-Disposition = %q, want oncall-ali.karimi.ics", cd)
	}

	// Unknown id -> 404.
	if rec := getICS(t, srv, "?user=nobody"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown user status = %d, want 404", rec.Code)
	}

	// Known person with no shifts in the window -> valid, empty 200 calendar.
	rec = getICS(t, srv, "?user=mahdi.idle")
	if rec.Code != http.StatusOK {
		t.Fatalf("idle-person status = %d, want 200", rec.Code)
	}
	if b := rec.Body.String(); !strings.Contains(b, "BEGIN:VCALENDAR") || strings.Contains(b, "BEGIN:VEVENT") {
		t.Errorf("idle-person feed should be a valid empty calendar:\n%s", b)
	}
}
