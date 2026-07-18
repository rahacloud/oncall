// Command oncall reports who is on-call over a Jalali date range, reading the
// canonical schedule-as-code YAML (the system of record that replaces the
// Confluence rotation table). See importer/ for the one-shot Confluence import.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rahacloud/oncall/internal/holiday"
	"github.com/rahacloud/oncall/internal/jalali"
	"github.com/rahacloud/oncall/internal/report"
	"github.com/rahacloud/oncall/internal/schedule"
)

const usageText = `oncall - on-call rotation reporter (schedule-as-code)

Usage:
  oncall [show] START END [flags]     per-shift printout (default)
  oncall csv    START END [-o FILE]   one row per day
  oncall count  START END             per-person day tally (working vs holiday)

Dates are Jalali, e.g. 1405/3/21. Ranges are inclusive.

Flags:
  --schedule PATH   schedule YAML (env ONCALL_SCHEDULE, default schedule.yaml)
  --no-holidays     skip holidayapi.ir; treat every day as a working day
  -o, --out FILE    (csv only) write to FILE instead of stdout
`

func main() {
	args := os.Args[1:]
	cmd := "show"
	if len(args) > 0 {
		switch args[0] {
		case "show", "csv", "count":
			cmd, args = args[0], args[1:]
		case "-h", "--help":
			fmt.Print(usageText)
			return
		}
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	schedPath := fs.String("schedule", envOr("ONCALL_SCHEDULE", "schedule.yaml"), "schedule YAML path")
	noHolidays := fs.Bool("no-holidays", false, "treat every day as a working day")
	var out string
	if cmd == "csv" {
		fs.StringVar(&out, "o", "", "output file (default stdout)")
		fs.StringVar(&out, "out", "", "output file (default stdout)")
	}

	// The flag package stops at the first positional, so split them ourselves
	// and let flags appear in any position (before or after the dates).
	flagArgs, rest := splitArgs(args)
	_ = fs.Parse(flagArgs)
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
		hol := holiday.Open(!*noHolidays)
		days, err := report.ResolveDays(sch, start, end, hol)
		check(err)
		hol.Save()
		if cmd == "csv" {
			check(report.CSV(days, out))
		} else {
			report.Count(os.Stdout, days, start, end, !*noHolidays)
		}
	}
}

// valueFlags are the flags that take a separate-argument value.
var valueFlags = map[string]bool{
	"-schedule": true, "--schedule": true,
	"-o": true, "--out": true,
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
