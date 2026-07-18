// Package report turns a schedule into the three views: show (per-shift),
// csv (per-day), and count (per-person tally, split working vs holiday).
package report

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rahacloud/oncall/internal/holiday"
	"github.com/rahacloud/oncall/internal/jalali"
	"github.com/rahacloud/oncall/internal/schedule"
)

// Day is one resolved calendar day in a range.
type Day struct {
	J           jalali.Date
	G           time.Time
	Weekday     string
	Person      string
	Shift       string
	Source      string
	Note        string
	IsHoliday   *bool
	HolidayName string
}

type interval struct {
	start, end time.Time
	person     string
	rotation   string
	handover   string
	note       string
}

func shiftIntervals(s *schedule.Schedule) ([]interval, error) {
	out := make([]interval, 0, len(s.Shifts))
	for _, sh := range s.Shifts {
		sd, err := jalali.Parse(sh.Start)
		if err != nil {
			return nil, err
		}
		ed, err := jalali.Parse(sh.End)
		if err != nil {
			return nil, err
		}
		out = append(out, interval{
			start: sd.ToTime(), end: ed.ToTime(), person: sh.Person,
			rotation: sh.Rotation, handover: sh.HandoverFrom,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	return out, nil
}

func overrideIntervals(s *schedule.Schedule) ([]interval, error) {
	out := make([]interval, 0, len(s.Overrides))
	for _, o := range s.Overrides {
		sd, err := jalali.Parse(o.Start)
		if err != nil {
			return nil, err
		}
		ed, err := jalali.Parse(o.End)
		if err != nil {
			return nil, err
		}
		out = append(out, interval{start: sd.ToTime(), end: ed.ToTime(), person: o.Person, note: o.Note})
	}
	return out, nil
}

func find(ivs []interval, t time.Time) *interval {
	for i := range ivs {
		if !t.Before(ivs[i].start) && !t.After(ivs[i].end) {
			return &ivs[i]
		}
	}
	return nil
}

// ResolveDays expands [start, end] inclusive into one record per day, applying
// overrides over shifts and classifying holidays.
func ResolveDays(s *schedule.Schedule, start, end jalali.Date, hol *holiday.Set) ([]Day, error) {
	shifts, err := shiftIntervals(s)
	if err != nil {
		return nil, err
	}
	ovs, err := overrideIntervals(s)
	if err != nil {
		return nil, err
	}
	var days []Day
	de := end.ToTime()
	for t := start.ToTime(); !t.After(de); t = t.AddDate(0, 0, 1) {
		jd := jalali.FromTime(t)
		d := Day{J: jd, G: t, Weekday: t.Weekday().String(), Person: "(gap)"}
		if iv := find(ovs, t); iv != nil {
			d.Person = s.DisplayName(iv.person)
			d.Source = "override"
			d.Note = iv.note
		} else if iv := find(shifts, t); iv != nil {
			d.Person = s.DisplayName(iv.person)
			d.Shift = iv.rotation
			d.Source = "schedule"
			if iv.handover != "" {
				d.Note = "handover from " + s.DisplayName(iv.handover)
			}
		}
		info := hol.Lookup(jd, t.Weekday())
		d.IsHoliday = info.IsHoliday
		d.HolidayName = info.Name
		days = append(days, d)
	}
	return days, nil
}

// Show prints the per-shift view, grouped by rotation label.
func Show(w io.Writer, s *schedule.Schedule, start, end jalali.Date) error {
	ds, de := start.ToTime(), end.ToTime()
	inRange := func(a, b string) (bool, error) {
		sd, err := jalali.Parse(a)
		if err != nil {
			return false, err
		}
		ed, err := jalali.Parse(b)
		if err != nil {
			return false, err
		}
		return !ed.ToTime().Before(ds) && !sd.ToTime().After(de), nil
	}

	fmt.Fprintf(w, "On-call %s -> %s (inclusive)\n%s\n", start, end, strings.Repeat("=", 52))

	var order []string
	groups := map[string][]schedule.Shift{}
	for _, sh := range s.Shifts {
		ok, err := inRange(sh.Start, sh.End)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, seen := groups[sh.Rotation]; !seen {
			order = append(order, sh.Rotation)
		}
		groups[sh.Rotation] = append(groups[sh.Rotation], sh)
	}

	var ovsIn []schedule.Override
	for _, o := range s.Overrides {
		ok, err := inRange(o.Start, o.End)
		if err != nil {
			return err
		}
		if ok {
			ovsIn = append(ovsIn, o)
		}
	}

	if len(order) == 0 && len(ovsIn) == 0 {
		fmt.Fprintln(w, "No shifts matched that window (check the range).")
		return nil
	}

	for _, rot := range order {
		label := rot
		if label == "" {
			label = "(unlabeled)"
		}
		fmt.Fprintf(w, "\nRotation «%s»\n", label)
		for _, sh := range groups[rot] {
			sd, _ := jalali.Parse(sh.Start)
			ed, _ := jalali.Parse(sh.End)
			note := ""
			if sh.HandoverFrom != "" {
				note = "   [handover from " + s.DisplayName(sh.HandoverFrom) + "]"
			}
			partial := ""
			if sd.ToTime().Before(ds) {
				partial = "  (window starts mid-shift)"
			} else if ed.ToTime().After(de) {
				partial = "  (window ends mid-shift)"
			}
			rng := fmt.Sprintf("%s - %s", sh.Start, sh.End)
			fmt.Fprintf(w, "  %-26s %s%s%s\n", rng, s.DisplayName(sh.Person), note, partial)
		}
	}

	if len(ovsIn) > 0 {
		fmt.Fprintf(w, "\nOverrides\n")
		for _, o := range ovsIn {
			note := ""
			if o.Note != "" {
				note = "   (" + o.Note + ")"
			}
			rng := fmt.Sprintf("%s - %s", o.Start, o.End)
			fmt.Fprintf(w, "  %-26s %s%s\n", rng, s.DisplayName(o.Person), note)
		}
	}
	return nil
}

func holStr(b *bool) string {
	if b == nil {
		return ""
	}
	if *b {
		return "true"
	}
	return "false"
}

// CSV writes one row per day to outPath (or stdout when empty).
func CSV(days []Day, outPath string) error {
	var w io.Writer = os.Stdout
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"jalali", "gregorian", "weekday", "person", "shift",
		"source", "is_holiday", "holiday_name", "note"})
	for _, d := range days {
		_ = cw.Write([]string{d.J.String(), d.G.Format("2006-01-02"), d.Weekday,
			d.Person, d.Shift, d.Source, holStr(d.IsHoliday), d.HolidayName, d.Note})
	}
	cw.Flush()
	if outPath != "" {
		fmt.Fprintf(os.Stderr, "wrote %s\n", outPath)
	}
	return cw.Error()
}

// PersonCount is one person's day tally over a range.
type PersonCount struct {
	Person  string `json:"person"`
	Total   int    `json:"total"`
	Working int    `json:"working"`
	Holiday int    `json:"holiday"`
}

// Tally aggregates resolved days into per-person counts, sorted by total desc.
func Tally(days []Day) []PersonCount {
	idx := map[string]*PersonCount{}
	var order []string
	for _, d := range days {
		if d.Person == "(gap)" || d.Person == "(empty)" || d.Person == "" {
			continue
		}
		t, ok := idx[d.Person]
		if !ok {
			t = &PersonCount{Person: d.Person}
			idx[d.Person] = t
			order = append(order, d.Person)
		}
		t.Total++
		if d.IsHoliday != nil && *d.IsHoliday {
			t.Holiday++
		} else {
			t.Working++
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return idx[order[i]].Total > idx[order[j]].Total })
	out := make([]PersonCount, 0, len(order))
	for _, name := range order {
		out = append(out, *idx[name])
	}
	return out
}

// Count prints the per-person day tally. With holidays enabled it splits the
// total into working vs holiday; otherwise it prints a plain day count.
func Count(w io.Writer, days []Day, start, end jalali.Date, showHolidays bool) {
	rows := Tally(days)
	fmt.Fprintf(w, "On-call day counts %s -> %s (inclusive)\n%s\n", start, end, strings.Repeat("=", 52))
	if len(rows) == 0 {
		fmt.Fprintln(w, "No shifts matched that window (check the range).")
		return
	}
	if showHolidays {
		fmt.Fprintf(w, "%-24s%7s%9s%9s\n", "person", "total", "working", "holiday")
		var tot, hol int
		for _, t := range rows {
			fmt.Fprintf(w, "%-24s%7d%9d%9d\n", t.Person, t.Total, t.Working, t.Holiday)
			tot, hol = tot+t.Total, hol+t.Holiday
		}
		fmt.Fprintln(w, strings.Repeat("-", 52))
		fmt.Fprintf(w, "%-24s%7d%9d%9d\n", "TOTAL", tot, tot-hol, hol)
	} else {
		fmt.Fprintf(w, "%-24s%7s\n", "person", "days")
		var tot int
		for _, t := range rows {
			fmt.Fprintf(w, "%-24s%7d\n", t.Person, t.Total)
			tot += t.Total
		}
		fmt.Fprintln(w, strings.Repeat("-", 32))
		fmt.Fprintf(w, "%-24s%7d\n", "TOTAL", tot)
	}
}
