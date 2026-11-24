// Package gpu exposes the editable GPU reference table and memory-bandwidth
// estimates. It is shared by hardware detection (to fill in missing VRAM and
// bandwidth) and scoring (to compute bandwidth/compute components).
package gpu

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"alocal/data"
)

// Entry is one row of data/gpus.json.
type Entry struct {
	Pattern       string  `json:"pattern"`
	Vendor        string  `json:"vendor"`
	Kind          string  `json:"kind"`
	VRAMGB        float64 `json:"vram_gb"`
	BandwidthGBps float64 `json:"bandwidth_gbps"`
	ComputeTier   float64 `json:"compute_tier"`

	re *regexp.Regexp
}

// Fallback is used when no pattern matches.
type Fallback struct {
	BandwidthGBps float64 `json:"bandwidth_gbps"`
	ComputeTier   float64 `json:"compute_tier"`
}

type file struct {
	Version        int                 `json:"version"`
	GPUs           []Entry             `json:"gpus"`
	VendorFallback map[string]Fallback `json:"vendor_fallbacks"`
}

// Table is the parsed GPU reference table.
type Table struct {
	Version   int
	Entries   []Entry
	Fallbacks map[string]Fallback
}

var (
	loadOnce  sync.Once
	cached    *Table
	cachedErr error
)

// Load parses the embedded hash table once.
func Load() (*Table, error) {
	loadOnce.Do(func() {
		cached, cachedErr = Parse(data.GPUsJSON)
	})
	return cached, cachedErr
}

// Parse parses a GPU table, compiling every pattern.
func Parse(b []byte) (*Table, error) {
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse gpu table: %w", err)
	}
	if len(f.GPUs) == 0 {
		return nil, fmt.Errorf("gpu table is empty")
	}
	t := &Table{Version: f.Version, Entries: make([]Entry, 0, len(f.GPUs)), Fallbacks: f.VendorFallback}
	for i, e := range f.GPUs {
		if strings.TrimSpace(e.Pattern) == "" {
			return nil, fmt.Errorf("gpu entry %d has an empty pattern", i)
		}
		re, err := regexp.Compile(e.Pattern)
		if err != nil {
			return nil, fmt.Errorf("gpu entry %d (%s): bad regex: %w", i, e.Pattern, err)
		}
		e.re = re
		t.Entries = append(t.Entries, e)
	}
	if t.Fallbacks == nil {
		t.Fallbacks = map[string]Fallback{}
	}
	return t, nil
}

// Lookup returns the first table row whose pattern matches name.
func (t *Table) Lookup(name, vendor string) (Entry, bool) {
	if t == nil {
		return Entry{}, false
	}
	for _, e := range t.Entries {
		if e.re != nil && e.re.MatchString(name) {
			return e, true
		}
	}
	return Entry{}, false
}

// LookupOrFallback returns a row, or a vendor fallback with Matched=false.
func (t *Table) LookupOrFallback(name, vendor string) (Entry, bool) {
	if e, ok := t.Lookup(name, vendor); ok {
		return e, true
	}
	fb, ok := t.FallbackFor(vendor)
	if !ok {
		return Entry{Vendor: vendor}, false
	}
	return Entry{Vendor: vendor, BandwidthGBps: fb.BandwidthGBps, ComputeTier: fb.ComputeTier}, false
}

// FallbackFor resolves vendor aliases case-insensitively.
func (t *Table) FallbackFor(vendor string) (Fallback, bool) {
	if t == nil {
		return Fallback{}, false
	}
	key := canonicalVendor(vendor)
	if fb, ok := t.Fallbacks[key]; ok {
		return fb, true
	}
	fb, ok := t.Fallbacks["unknown"]
	return fb, ok
}

func canonicalVendor(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case hasWord(v, "nvidia"), hasWord(v, "geforce"), hasWord(v, "quadro"), hasWord(v, "tesla"):
		return "NVIDIA"
	case hasWord(v, "amd"), hasWord(v, "radeon"), hasWord(v, "ati"), strings.Contains(v, "advanced micro devices"):
		return "AMD"
	case hasWord(v, "apple"):
		return "Apple"
	case hasWord(v, "intel"):
		return "Intel"
	}
	return "unknown"
}

func hasWord(s, word string) bool {
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if f == word {
			return true
		}
	}
	return false
}

// CanonicalVendor normalizes a vendor string.
func CanonicalVendor(v string) string { return canonicalVendor(v) }

// EstimateRAMBandwidth estimates theoretical dual-channel bandwidth from the
// memory type and speed. This is a reference value, not a measurement.
func EstimateRAMBandwidth(memType string, speedMTs int) float64 {
	mt := strings.ToUpper(strings.TrimSpace(memType))
	if mt == "" && speedMTs == 0 {
		return 30 // conservative default when nothing is known
	}
	if speedMTs <= 0 {
		switch {
		case strings.Contains(mt, "DDR5"):
			speedMTs = 5600
		case strings.Contains(mt, "DDR4"):
			speedMTs = 3200
		case strings.Contains(mt, "DDR3"):
			speedMTs = 1600
		case strings.Contains(mt, "LPDDR5"):
			speedMTs = 6400
		case strings.Contains(mt, "LPDDR4"):
			speedMTs = 4266
		default:
			speedMTs = 3200
		}
	}
	// 8 bytes per transfer, two channels. MT/s * 8 * 2 / 1000 = GB/s.
	bw := float64(speedMTs) * 16.0 / 1000.0
	if bw < 5 {
		bw = 5
	}
	if bw > 400 {
		bw = 400
	}
	return bw
}
