// Package server exposes the schedule over HTTP: a read API, a small web UI, an
// ICS calendar feed, and token-guarded mutation endpoints (add/remove overrides
// and shifts). It is the web-service face of the schedule-as-code data.
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/rahacloud/oncall/internal/holiday"
	"github.com/rahacloud/oncall/internal/jalali"
	"github.com/rahacloud/oncall/internal/report"
	"github.com/rahacloud/oncall/internal/schedule"
	"github.com/rahacloud/oncall/internal/store"
)

// Server bundles the store, holiday set, and auth config into an http.Handler.
type Server struct {
	store *store.Store
	hol   *holiday.Set
	token string // bearer token required for mutations; "" = read-only
	mux   *http.ServeMux
}

// New wires up the routes. When token is empty, mutation endpoints return 403.
func New(st *store.Store, hol *holiday.Set, token string) *Server {
	s := &Server{store: st, hol: hol, token: token, mux: http.NewServeMux()}
	m := s.mux

	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	m.HandleFunc("GET /{$}", s.handleIndex)

	m.HandleFunc("GET /api/schedule", s.handleSchedule)
	m.HandleFunc("GET /api/current", s.handleCurrent)
	m.HandleFunc("GET /api/current.txt", s.handleCurrentText)
	m.HandleFunc("GET /api/range", s.handleRange)
	m.HandleFunc("GET /api/count", s.handleCount)
	m.HandleFunc("GET /calendar.ics", s.handleICS)

	m.HandleFunc("POST /api/overrides", s.auth(s.handleAddOverride))
	m.HandleFunc("DELETE /api/overrides/{index}", s.auth(s.handleDeleteOverride))
	m.HandleFunc("POST /api/shifts", s.auth(s.handleAddShift))
	m.HandleFunc("PUT /api/people/{id}", s.auth(s.handleUpsertPerson))

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// --- helpers ---------------------------------------------------------------

type dayDTO struct {
	Jalali      string `json:"jalali"`
	Gregorian   string `json:"gregorian"`
	Weekday     string `json:"weekday"`
	Person      string `json:"person"`
	Shift       string `json:"shift"`
	Source      string `json:"source"`
	Note        string `json:"note"`
	IsHoliday   *bool  `json:"is_holiday"`
	HolidayName string `json:"holiday_name"`
}

func toDTO(d report.Day) dayDTO {
	return dayDTO{
		Jalali: d.J.String(), Gregorian: d.G.Format("2006-01-02"), Weekday: d.Weekday,
		Person: d.Person, Shift: d.Shift, Source: d.Source, Note: d.Note,
		IsHoliday: d.IsHoliday, HolidayName: d.HolidayName,
	}
}

func today() jalali.Date { return jalali.FromTime(time.Now()) }

func (s *Server) resolve(start, end jalali.Date) ([]report.Day, error) {
	return report.ResolveDays(s.store.Snapshot(), start, end, s.hol)
}

func dateParam(r *http.Request, name string, def jalali.Date) (jalali.Date, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	return jalali.Parse(v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "mutations disabled: set ONCALL_TOKEN to enable"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+s.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h(w, r)
	}
}

// --- read handlers ---------------------------------------------------------

func (s *Server) handleSchedule(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Snapshot())
}

func (s *Server) handleCurrent(w http.ResponseWriter, r *http.Request) {
	d, err := dateParam(r, "date", today())
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	days, err := s.resolve(d, d)
	if err != nil || len(days) == 0 {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve failed"})
		return
	}
	writeJSON(w, http.StatusOK, toDTO(days[0]))
}

func (s *Server) handleCurrentText(w http.ResponseWriter, r *http.Request) {
	d, err := dateParam(r, "date", today())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	days, err := s.resolve(d, d)
	if err != nil || len(days) == 0 {
		http.Error(w, "resolve failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(days[0].Person))
}

func (s *Server) handleRange(w http.ResponseWriter, r *http.Request) {
	start, err := dateParam(r, "start", today())
	if err != nil {
		badRequest(w, "bad start: "+err.Error())
		return
	}
	end, err := dateParam(r, "end", start)
	if err != nil {
		badRequest(w, "bad end: "+err.Error())
		return
	}
	if end.ToTime().Before(start.ToTime()) {
		badRequest(w, "end before start")
		return
	}
	days, err := s.resolve(start, end)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	out := make([]dayDTO, len(days))
	for i, d := range days {
		out[i] = toDTO(d)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCount(w http.ResponseWriter, r *http.Request) {
	start, err := dateParam(r, "start", today())
	if err != nil {
		badRequest(w, "bad start: "+err.Error())
		return
	}
	end, err := dateParam(r, "end", start)
	if err != nil {
		badRequest(w, "bad end: "+err.Error())
		return
	}
	if end.ToTime().Before(start.ToTime()) {
		badRequest(w, "end before start")
		return
	}
	days, err := s.resolve(start, end)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report.Tally(days))
}

// --- mutation handlers -----------------------------------------------------

func validRange(start, end string) error {
	if _, err := jalali.Parse(start); err != nil {
		return err
	}
	_, err := jalali.Parse(end)
	return err
}

func (s *Server) handleAddOverride(w http.ResponseWriter, r *http.Request) {
	var o schedule.Override
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		badRequest(w, "bad json: "+err.Error())
		return
	}
	if o.Person == "" || validRange(o.Start, o.End) != nil {
		badRequest(w, "need valid start, end (Jalali) and person")
		return
	}
	if err := s.store.AddOverride(o); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

func (s *Server) handleDeleteOverride(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		badRequest(w, "index must be an integer")
		return
	}
	if err := s.store.DeleteOverride(idx); err != nil {
		badRequest(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAddShift(w http.ResponseWriter, r *http.Request) {
	var sh schedule.Shift
	if err := json.NewDecoder(r.Body).Decode(&sh); err != nil {
		badRequest(w, "bad json: "+err.Error())
		return
	}
	if sh.Person == "" || validRange(sh.Start, sh.End) != nil {
		badRequest(w, "need valid start, end (Jalali) and person")
		return
	}
	if err := s.store.AddShift(sh); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, sh)
}

func (s *Server) handleUpsertPerson(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p schedule.Person
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		badRequest(w, "bad json: "+err.Error())
		return
	}
	if err := s.store.UpsertPerson(id, p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "person": p})
}
