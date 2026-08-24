// Command oncall reports who is on-call over a Jalali date range, reading the
// canonical schedule-as-code YAML (the system of record that replaces the
// Confluence rotation table). See importer/ for the one-shot Confluence import.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rahacloud/oncall/internal/holiday"
	"github.com/rahacloud/oncall/internal/jalali"
	"github.com/rahacloud/oncall/internal/report"
	"github.com/rahacloud/oncall/internal/schedule"
	"github.com/rahacloud/oncall/internal/server"
	"github.com/rahacloud/oncall/internal/store"
	"github.com/rahacloud/oncall/internal/watch"
)

const usageText = `oncall - on-call rotation reporter (schedule-as-code)

Usage:
  oncall [show] START END [flags]     per-shift printout (default)
  oncall csv    START END [-o FILE]   one row per day
  oncall count  START END             per-person day tally (working vs holiday)
  oncall serve  [flags]               HTTP API + web UI + ICS calendar feed

Dates are Jalali, e.g. 1405/3/21. Ranges are inclusive.

Flags:
  --schedule PATH   schedule YAML (env ONCALL_SCHEDULE, default schedule.yaml)
  --holidays PATH   holidays YAML (env ONCALL_HOLIDAYS); off when unset
  -o, --out FILE    (csv only) write to FILE instead of stdout
  --addr ADDR       (serve only) listen address (env ONCALL_ADDR, default :8080)
  --watch DUR       (serve only) reload files this often; 0 disables
                    (env ONCALL_WATCH_INTERVAL, default 2s)

Holidays are read from a local file (no network); see holidays.example.yaml.
serve reads the mutation token from $ONCALL_TOKEN; when unset, the API is
read-only (mutation endpoints return 403). While serving, edits to the schedule
and holidays files are picked up automatically -- no restart needed.
`

func main() {
	args := os.Args[1:]
	cmd := "show"
	if len(args) > 0 {
		switch args[0] {
		case "show", "csv", "count", "serve":
			cmd, args = args[0], args[1:]
		case "-h", "--help":
			fmt.Print(usageText)
			return
		}
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	schedPath := fs.String("schedule", envOr("ONCALL_SCHEDULE", "schedule.yaml"), "schedule YAML path")
	holidaysPath := fs.String("holidays", os.Getenv("ONCALL_HOLIDAYS"), "holidays YAML path")
	var out, addr, watchDur string
	if cmd == "csv" {
		fs.StringVar(&out, "o", "", "output file (default stdout)")
		fs.StringVar(&out, "out", "", "output file (default stdout)")
	}
	if cmd == "serve" {
		fs.StringVar(&addr, "addr", envOr("ONCALL_ADDR", ":8080"), "listen address")
		fs.StringVar(&watchDur, "watch", envOr("ONCALL_WATCH_INTERVAL", "2s"), "file reload interval; 0 disables")
	}

	// The flag package stops at the first positional, so split them ourselves
	// and let flags appear in any position (before or after the dates).
	flagArgs, rest := splitArgs(args)
	_ = fs.Parse(flagArgs)

	if cmd == "serve" {
		watchInterval, err := time.ParseDuration(watchDur)
		if err != nil {
			fatal(fmt.Sprintf("bad --watch %q: %v", watchDur, err))
		}
		serve(*schedPath, *holidaysPath, addr, watchInterval)
		return
	}
	if len(rest) < 2 {
		fatal("need START and END Jalali dates, e.g. oncall 1405/3/21 1405/4/20")
	}
	start, err := jalali.Parse(rest[0])
	check(err)
	end, err := jalali.Parse(rest[1])
	check(err)
	if end.ToTime().Before(start.ToTime()) {
		fatal("end date is before start date")
	}

	sch, err := schedule.Load(*schedPath)
	if err != nil {
		fatal(fmt.Sprintf("load schedule: %v", err))
	}

	switch cmd {
	case "show":
		check(report.Show(os.Stdout, sch, start, end))
	case "csv", "count":
		hol, err := holiday.Load(*holidaysPath)
		check(err)
		days, err := report.ResolveDays(sch, start, end, hol)
		check(err)
		if cmd == "csv" {
			check(report.CSV(days, out))
		} else {
			report.Count(os.Stdout, days, start, end, hol.Enabled())
		}
	}
}

// valueFlags are the flags that take a separate-argument value.
var valueFlags = map[string]bool{
	"-schedule": true, "--schedule": true,
	"-holidays": true, "--holidays": true,
	"-o": true, "--out": true,
	"-addr": true, "--addr": true,
}

// splitArgs separates flag tokens (and their values) from positional args, so
// flags may appear anywhere on the command line.
func splitArgs(args []string) (flags, pos []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 0 && a[0] == '-' {
			flags = append(flags, a)
			if !contains(a, '=') && valueFlags[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, a)
		}
	}
	return flags, pos
}

func contains(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

func serve(schedPath, holidaysPath, addr string, watchInterval time.Duration) {
	st, err := store.Open(schedPath)
	if err != nil {
		fatal(fmt.Sprintf("load schedule: %v", err))
	}
	hol, err := holiday.Load(holidaysPath)
	check(err)
	token := os.Getenv("ONCALL_TOKEN")
	srv := server.New(st, hol, token)

	watchNote := "watch off"
	if watchInterval > 0 {
		w := watch.New(watchInterval, func(f string, a ...any) {
			fmt.Fprintf(os.Stderr, "oncall: "+f+"\n", a...)
		})
		w.Add(schedPath, st.Reload)
		if holidaysPath != "" {
			w.Add(holidaysPath, func() error {
				ns, err := holiday.Load(holidaysPath)
				if err != nil {
					return err
				}
				srv.SetHolidays(ns)
				return nil
			})
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go w.Run(ctx)
		watchNote = "watching files every " + watchInterval.String()
	}

	mode := "read-only (set ONCALL_TOKEN to enable writes)"
	if token != "" {
		mode = "read-write (token set)"
	}
	holNote := "holidays off"
	if hol.Enabled() {
		holNote = "holidays from " + holidaysPath
	}
	fmt.Fprintf(os.Stderr, "oncall serving %s on %s — %s, %s, %s\n", schedPath, addr, mode, holNote, watchNote)
	if err := http.ListenAndServe(addr, srv); err != nil {
		fatal(err.Error())
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func check(err error) {
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "oncall: "+msg)
	os.Exit(1)
}
