// Package holiday classifies Jalali days as holiday vs working from a local,
// user-provided description file. There is no network dependency: if no file is
// given, holiday classification is simply off (every day counts as working).
//
// File format (YAML):
//
//	# recurring weekly non-working days (Go weekday names, e.g. Friday)
//	weekends: [Friday]
//	# specific Jalali holiday dates (YYYY-MM-DD) -> name (name may be empty)
//	dates:
//	  "1405-01-01": Nowruz
//	  "1405-01-12": Islamic Republic Day
package holiday

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rahacloud/oncall/internal/jalali"
	"gopkg.in/yaml.v3"
)

// Info is the classification of a single day.
type Info struct {
	IsHoliday *bool // nil = unknown; here always non-nil
	Name      string
}

// Set is an immutable, in-memory holiday table.
type Set struct {
	enabled  bool
	weekends map[time.Weekday]bool
	dates    map[string]string // canonical jalali key -> name
}

type file struct {
	Weekends []string          `yaml:"weekends"`
	Dates    map[string]string `yaml:"dates"`
}

var weekdayNames = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
	"saturday": time.Saturday,
}

// Load reads a holiday file. An empty path (or a path that does not exist)
// yields a disabled Set — holidays off, no error. A present-but-invalid file is
// an error.
func Load(path string) (*Set, error) {
	if path == "" {
		return &Set{enabled: false}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "oncall: holidays file %s not found; holiday classification disabled\n", path)
			return &Set{enabled: false}, nil
		}
		return nil, err
	}
	var f file
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse holidays %s: %w", path, err)
	}
	s := &Set{enabled: true, weekends: map[time.Weekday]bool{}, dates: map[string]string{}}
	for _, w := range f.Weekends {
		wd, ok := weekdayNames[strings.ToLower(strings.TrimSpace(w))]
		if !ok {
			return nil, fmt.Errorf("holidays %s: unknown weekday %q", path, w)
		}
		s.weekends[wd] = true
	}
	for k, name := range f.Dates {
		jd, err := jalali.Parse(k)
		if err != nil {
			return nil, fmt.Errorf("holidays %s: bad date %q: %w", path, k, err)
		}
		s.dates[jd.String()] = name // canonical YYYY/MM/DD key
	}
	return s, nil
}

// Enabled reports whether holiday classification is active.
func (s *Set) Enabled() bool { return s.enabled }

// Lookup classifies a day given its Jalali date and Gregorian weekday.
func (s *Set) Lookup(d jalali.Date, wd time.Weekday) Info {
	t, f := true, false
	if !s.enabled {
		return Info{IsHoliday: &f}
	}
	if name, ok := s.dates[d.String()]; ok {
		if name == "" {
			name = "Holiday"
		}
		return Info{IsHoliday: &t, Name: name}
	}
	if s.weekends[wd] {
		return Info{IsHoliday: &t, Name: "Weekend"}
	}
	return Info{IsHoliday: &f}
}
