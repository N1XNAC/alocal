package hardware

import (
	"strings"

	"llmfit/internal/gpu"
)

func hasVendorGPU(p *Profile, vendor string) bool {
	for _, g := range p.GPUs {
		if gpu.CanonicalVendor(g.Vendor) == vendor {
			return true
		}
	}
	return false
}

func hasSameGPUName(p *Profile, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, g := range p.GPUs {
		if strings.EqualFold(strings.TrimSpace(g.Name), name) {
			return true
		}
	}
	key := significantNameToken(name)
	if key == "" {
		return false
	}
	for _, g := range p.GPUs {
		if strings.Contains(strings.ToLower(g.Name), key) {
			return true
		}
	}
	return false
}

func gpuVendorFromName(s string) string {
	return gpu.CanonicalVendor(s)
}

func significantNameToken(name string) string {
	lower := strings.ToLower(name)
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	for _, f := range fields {
		if len(f) >= 3 && strings.IndexFunc(f, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 {
			return f
		}
	}
	return ""
}
