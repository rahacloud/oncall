package server

import (
	_ "embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/rahacloud/oncall/internal/jalali"
)

//go:embed static/index.html
var indexHTML []byte

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// handleICS emits an all-day VEVENT per shift, so the schedule can be subscribed
// to from any calendar app. All-day DTEND is exclusive, hence end+1 day.
func (s *Server) handleICS(w http.ResponseWriter, _ *http.Request) {
	sch := s.store.Snapshot()
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//rahacloud//oncall//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\nX-WR-CALNAME:On-Call\r\n")
	for i, sh := range sch.Shifts {
		sd, err := jalali.Parse(sh.Start)
		if err != nil {
			continue
		}
		ed, err := jalali.Parse(sh.End)
		if err != nil {
			continue
		}
		start := sd.ToTime()
		endExclusive := ed.ToTime().AddDate(0, 0, 1)
		summary := "On-call: " + sch.DisplayName(sh.Person)
		desc := sh.Rotation
		if sh.HandoverFrom != "" {
			desc = strings.TrimSpace(desc + " (handover from " + sch.DisplayName(sh.HandoverFrom) + ")")
		}
		b.WriteString("BEGIN:VEVENT\r\n")
		fmt.Fprintf(&b, "UID:oncall-%d-%s@rahacloud\r\n", i, sh.Start)
		fmt.Fprintf(&b, "DTSTART;VALUE=DATE:%s\r\n", start.Format("20060102"))
		fmt.Fprintf(&b, "DTEND;VALUE=DATE:%s\r\n", endExclusive.Format("20060102"))
		fmt.Fprintf(&b, "SUMMARY:%s\r\n", icsEscape(summary))
		if desc != "" {
			fmt.Fprintf(&b, "DESCRIPTION:%s\r\n", icsEscape(desc))
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="oncall.ics"`)
	w.Write([]byte(b.String()))
}

func icsEscape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", "\n", "\\n")
	return r.Replace(s)
}
