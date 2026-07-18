// Package schedule loads the canonical, version-controlled on-call schedule.
// This YAML file is the system of record -- it replaces the Confluence table.
package schedule

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Person is a rotation participant, keyed by a stable id in the People map.
type Person struct {
	Name string `yaml:"name" json:"name"`
}

// Shift assigns one person to an inclusive Jalali date range.
type Shift struct {
	Start        string `yaml:"start" json:"start"`
	End          string `yaml:"end" json:"end"`
	Person       string `yaml:"person" json:"person"`
	Rotation     string `yaml:"rotation,omitempty" json:"rotation,omitempty"`           // cosmetic grouping label
	HandoverFrom string `yaml:"handover_from,omitempty" json:"handover_from,omitempty"` // shift handed off mid-way
}

// Override reassigns whoever is on-call for an inclusive Jalali range. Overrides
// win over shifts for any overlapping day -- this is how swaps are expressed.
type Override struct {
	Start  string `yaml:"start" json:"start"`
	End    string `yaml:"end" json:"end"`
	Person string `yaml:"person" json:"person"`
	Note   string `yaml:"note,omitempty" json:"note,omitempty"`
}

// Schedule is the whole file.
type Schedule struct {
	People    map[string]Person `yaml:"people" json:"people"`
	Shifts    []Shift           `yaml:"shifts" json:"shifts"`
	Overrides []Override        `yaml:"overrides" json:"overrides"`
}

// Load reads and parses a schedule YAML file.
func Load(path string) (*Schedule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Schedule
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}

// DisplayName returns a person's name, falling back to their id.
func (s *Schedule) DisplayName(id string) string {
	if p, ok := s.People[id]; ok && p.Name != "" {
		return p.Name
	}
	return id
}

// Save atomically writes the schedule back to a YAML file.
func (s *Schedule) Save(path string) error {
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	header := []byte("# Managed by oncall -- schedule-as-code (system of record).\n" +
		"# Dates are Jalali YYYY-MM-DD, ranges inclusive.\n")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(header, b...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Clone returns a deep copy so readers never race with in-place mutations.
func (s *Schedule) Clone() *Schedule {
	c := &Schedule{
		People:    make(map[string]Person, len(s.People)),
		Shifts:    append([]Shift(nil), s.Shifts...),
		Overrides: append([]Override(nil), s.Overrides...),
	}
	for k, v := range s.People {
		c.People[k] = v
	}
	return c
}
