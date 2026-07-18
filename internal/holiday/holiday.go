// Package holiday classifies Jalali days as holiday vs working using
// holidayapi.ir, with an on-disk cache. Fridays and official holidays both come
// back as holidays. Lookups are best-effort: a failed fetch yields an unknown
// (nil) result rather than an error.
package holiday

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rahacloud/oncall/internal/jalali"
)

const api = "https://holidayapi.ir/jalali"

// Info is the classification of a single day.
type Info struct {
	IsHoliday *bool // nil = unknown (fetch failed)
	Events    []string
}

type record struct {
	IsHoliday bool     `json:"is_holiday"`
	Events    []string `json:"events"`
}

// Cache memoizes holiday lookups to ~/.cache/oncall/holidays.json.
type Cache struct {
	path    string
	data    map[string]record
	dirty   bool
	client  *http.Client
	enabled bool
}

// Open loads the cache. When enabled is false, every day is reported as working
// and no network calls are made.
func Open(enabled bool) *Cache {
	c := &Cache{
		path: filepath.Join(cacheDir(), "holidays.json"),
		data: map[string]record{},
		// holidayapi.ir is an external service: honor the standard proxy env
		// (HTTP(S)_PROXY) so this works both direct and behind a corp proxy.
		client:  &http.Client{Timeout: 20 * time.Second},
		enabled: enabled,
	}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &c.data)
	}
	return c
}

func cacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "oncall")
	}
	return ".oncall-cache"
}

// Lookup classifies a Jalali date.
func (c *Cache) Lookup(d jalali.Date) Info {
	if !c.enabled {
		f := false
		return Info{IsHoliday: &f}
	}
	key := d.String()
	if r, ok := c.data[key]; ok {
		h := r.IsHoliday
		return Info{IsHoliday: &h, Events: r.Events}
	}
	r, ok := c.fetch(d)
	if !ok {
		return Info{} // unknown; not cached
	}
	c.data[key] = r
	c.dirty = true
	h := r.IsHoliday
	return Info{IsHoliday: &h, Events: r.Events}
}

func (c *Cache) fetch(d jalali.Date) (record, bool) {
	url := fmt.Sprintf("%s/%d/%02d/%02d", api, d.Y, d.M, d.D)
	resp, err := c.client.Get(url)
	if err != nil {
		return record{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return record{}, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return record{}, false
	}
	var raw struct {
		IsHoliday bool `json:"is_holiday"`
		Events    []struct {
			Description string `json:"description"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return record{}, false
	}
	rec := record{IsHoliday: raw.IsHoliday}
	for _, e := range raw.Events {
		if e.Description != "" {
			rec.Events = append(rec.Events, e.Description)
		}
	}
	return rec, true
}

// Save persists the cache if anything new was fetched.
func (c *Cache) Save() {
	if !c.dirty {
		return
	}
	_ = os.MkdirAll(filepath.Dir(c.path), 0o755)
	if b, err := json.MarshalIndent(c.data, "", " "); err == nil {
		_ = os.WriteFile(c.path, b, 0o644)
	}
}
