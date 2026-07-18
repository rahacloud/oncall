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
	Name string `yaml:"name"`
}

// Shift assigns one person to an inclusive Jalali date range.
type Shift struct {
	Start        string `yaml:"start"`
	End          string `yaml:"end"`
	Person       string `yaml:"person"`
	Rotation     string `yaml:"rotation,omitempty"`      // cosmetic grouping label
	HandoverFrom string `yaml:"handover_from,omitempty"` // shift handed off mid-way
}

// Override reassigns whoever is on-call for an inclusive Jalali range. Overrides
// win over shifts for any overlapping day -- this is how swaps are expressed.
type Override struct {
	Start  string `yaml:"start"`
	End    string `yaml:"end"`
	Person string `yaml:"person"`
	Note   string `yaml:"note,omitempty"`
}

// Schedule is the whole file.
type Schedule struct {
	People    map[string]Person `yaml:"people"`
	Shifts    []Shift           `yaml:"shifts"`
	Overrides []Override        `yaml:"overrides"`
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
