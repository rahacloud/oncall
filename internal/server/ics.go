package server

import (
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rahacloud/oncall/internal/jalali"
	"github.com/rahacloud/oncall/internal/report"
	"github.com/rahacloud/oncall/internal/schedule"
)

//go:embed static/index.html
var indexHTML []byte

// Feed tuning. The window is resolved fresh on every request, so it always
// tracks "today"; the refresh interval is the poll cadence advertised to
// subscribing calendar apps (Google/Apple/Outlook).
const (
	icsPastDays   = 90
	icsFutureDays = 400
	icsProdID     = "-//rahacloud//oncall//EN"
	icsRefresh    = "PT1H" // RFC 5545 DURATION: re-poll hourly
)

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// handleICS serves the schedule as a live, subscribable RFC 5545 (iCalendar)
// feed. It resolves shifts + overrides + holidays over a rolling window and
// coalesces contiguous same-assignment days into one all-day VEVENT, so the
// feed matches what the web UI/API show. The body is byte-stable between
// mutations (DTSTAMP is seeded from the store's data version), which makes the
// ETag/If-None-Match conditional-GET dance meaningful for polling clients.
//
// ?user=<id> narrows the feed to one person (matched by schedule id,
// case-insensitively); an unknown id is a 404, a known id with no shifts in the
// window is a valid empty calendar. ?download=1 flips Content-Disposition to
// attachment (one-time import); otherwise it is served inline for webcal://
// subscription.
func (s *Server) handleICS(w http.ResponseWriter, r *http.Request) {
	sch, version := s.store.SnapshotWithVersion()

	now := time.Now()
	start := jalali.FromTime(now.AddDate(0, 0, -icsPastDays))
	end := jalali.FromTime(now.AddDate(0, 0, icsFutureDays))

	days, err := report.ResolveDays(sch, start, end, s.hol.Load())
	if err != nil {
		http.Error(w, "resolve failed", http.StatusInternalServerError)
		return
	}

	calName, filename := "On-Call", "oncall.ics"
	if user := strings.TrimSpace(r.URL.Query().Get("user")); user != "" {
		id, ok := knownPersonIDs(sch)[strings.ToLower(user)]
		if !ok {
			http.Error(w, "unknown user: "+user, http.StatusNotFound)
			return
		}
		days = filterByPersonID(days, id)
		calName = "On-Call — " + sch.DisplayName(id)
		filename = "oncall-" + id + ".ics"
	}
	body := buildICS(days, version, calName)

	sum := sha1.Sum(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`

	disposition := "inline"
	if r.URL.Query().Get("download") != "" {
		disposition = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", "text/calendar; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("ETag", etag)
	h.Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, filename))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(body)
}

// knownPersonIDs maps every known person id (lowercased) to its canonical id. A
// person counts as known if they key the people map or are referenced by any
// shift or override -- someone can have shifts without a people entry.
func knownPersonIDs(sch *schedule.Schedule) map[string]string {
	ids := make(map[string]string)
	add := func(id string) {
		if id != "" {
			ids[strings.ToLower(id)] = id
		}
	}
	for id := range sch.People {
		add(id)
	}
	for _, sh := range sch.Shifts {
		add(sh.Person)
	}
	for _, o := range sch.Overrides {
		add(o.Person)
	}
	return ids
}

// filterByPersonID keeps only the days assigned to the given person id.
func filterByPersonID(days []report.Day, id string) []report.Day {
	out := days[:0:0]
	for _, d := range days {
		if strings.EqualFold(d.PersonID, id) {
			out = append(out, d)
		}
	}
	return out
}

// run is a maximal streak of consecutive days sharing the same assignment.
type run struct {
	start, end time.Time // inclusive Gregorian bounds
	person     string    // already a display name (report resolves ids)
	source     string    // "override" | "schedule"
	shift      string    // rotation label, if any
	note       string    // handover / swap note, if any
}

// coalesce collapses per-day resolution into multi-day runs, skipping gaps. The
// contiguity check keeps a skipped gap day from silently merging its neighbours.
func coalesce(days []report.Day) []run {
	var runs []run
	for _, d := range days {
		if d.Person == "(gap)" || d.Person == "(empty)" || d.Person == "" {
			continue
		}
		if n := len(runs); n > 0 {
			last := &runs[n-1]
			if last.person == d.Person && last.source == d.Source &&
				last.shift == d.Shift && last.note == d.Note &&
				d.G.Equal(last.end.AddDate(0, 0, 1)) {
				last.end = d.G
				continue
			}
		}
		runs = append(runs, run{
			start: d.G, end: d.G, person: d.Person,
			source: d.Source, shift: d.Shift, note: d.Note,
		})
	}
	return runs
}

func buildICS(days []report.Day, version time.Time, calName string) []byte {
	stamp := version.UTC().Format("20060102T150405Z")
	seq := version.Unix()

	var b strings.Builder
	writeLine(&b, "BEGIN:VCALENDAR")
	writeLine(&b, "VERSION:2.0")
	writeLine(&b, "PRODID:"+icsProdID)
	writeLine(&b, "CALSCALE:GREGORIAN")
	writeLine(&b, "METHOD:PUBLISH")
	writeLine(&b, "X-WR-CALNAME:"+icsEscape(calName))
	writeLine(&b, "X-WR-TIMEZONE:UTC")
	writeLine(&b, "REFRESH-INTERVAL;VALUE=DURATION:"+icsRefresh)
	writeLine(&b, "X-PUBLISHED-TTL:"+icsRefresh)

	for _, rn := range coalesce(days) {
		endExclusive := rn.end.AddDate(0, 0, 1) // all-day DTEND is exclusive
		desc := rn.shift
		if rn.source == "override" {
			desc = rn.note
		} else if rn.note != "" {
			desc = strings.TrimSpace(rn.shift + " (" + rn.note + ")")
		}

		writeLine(&b, "BEGIN:VEVENT")
		writeLine(&b, "UID:"+uid(rn))
		writeLine(&b, "DTSTAMP:"+stamp)
		writeLine(&b, "LAST-MODIFIED:"+stamp)
		writeLine(&b, fmt.Sprintf("SEQUENCE:%d", seq))
		writeLine(&b, "DTSTART;VALUE=DATE:"+rn.start.Format("20060102"))
		writeLine(&b, "DTEND;VALUE=DATE:"+endExclusive.Format("20060102"))
		writeLine(&b, "SUMMARY:"+icsEscape("On-call: "+rn.person))
		if desc != "" {
			writeLine(&b, "DESCRIPTION:"+icsEscape(desc))
		}
		writeLine(&b, "END:VEVENT")
	}
	writeLine(&b, "END:VCALENDAR")
	return []byte(b.String())
}

// uid derives a stable, content-based UID keyed on a run's identity (start date
// + person + source). Because it does not depend on slice position, inserting
// or reordering shifts no longer makes subscribers churn events; extending a
// run keeps the UID and lets LAST-MODIFIED/SEQUENCE signal the update.
func uid(rn run) string {
	sum := sha1.Sum([]byte(rn.start.Format("20060102") + "|" + rn.person + "|" + rn.source))
	return "oncall-" + hex.EncodeToString(sum[:8]) + "@rahacloud"
}

// writeLine folds one logical content line to <=75 octets per RFC 5545 §3.1
// (continuation lines start with a space) and terminates it with CRLF. Folding
// on rune boundaries keeps multi-byte UTF-8 (e.g. Persian names) intact.
func writeLine(b *strings.Builder, line string) {
	const max = 75
	n := 0
	for _, r := range line {
		rl := utf8.RuneLen(r)
		if n+rl > max {
			b.WriteString("\r\n ")
			n = 1 // the leading space counts toward the continuation line
		}
		b.WriteRune(r)
		n += rl
	}
	b.WriteString("\r\n")
}

func icsEscape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", "\n", "\\n")
	return r.Replace(s)
}
