//go:build linux

package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"alocal/internal/gpu"
)

func (d SystemDetector) detectPlatformGPUs(ctx context.Context, p *Profile) {
	d.detectAMDGPUs(ctx, p)
	d.detectLinuxGPUFallback(ctx, p)
}

func (d SystemDetector) detectAMDGPUs(ctx context.Context, p *Profile) {
	out, err := d.runCmd(ctx, p, "rocm-smi", "--showproductname", "--showmeminfo", "vram", "--showdriverversion", "--json")
	if err == nil {
		if n := parseROCMJSON(p, out); n > 0 {
			return
		}
	}
	d.detectAMDviaSysfs(ctx, p)
}

func parseROCMJSON(p *Profile, out string) int {
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		return 0
	}
	count := 0
	for key, raw := range root {
		if !strings.HasPrefix(strings.ToLower(key), "card") {
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := mapValueContains(m, "card series")
		if name == "" {
			name = mapValueContains(m, "card model")
		}
		if name == "" {
			name = mapValueContains(m, "device name")
		}
		if name == "" {
			name = key
		}
		vram := mapValueContains(m, "vram total memory")
		used := mapValueContains(m, "vram total used memory")
		driver := mapValueContains(m, "driver version")
		totalGB := bytesValueToGB(vram)
		freeGB := 0.0
		if totalGB > 0 && bytesValueToGB(used) > 0 {
			freeGB = totalGB - bytesValueToGB(used)
			if freeGB < 0 {
				freeGB = 0
			}
		}
		p.GPUs = append(p.GPUs, GPUInfo{
			Vendor: "AMD", Name: name, Kind: "discrete",
			VRAMTotalGB: totalGB, VRAMFreeGB: freeGB, Driver: driver,
			Source: "rocm-smi",
		})
		count++
	}
	return count
}

func mapValueContains(m map[string]any, needle string) string {
	needle = strings.ToLower(needle)
	for k, v := range m {
		if !strings.Contains(strings.ToLower(k), needle) {
			continue
		}
		switch x := v.(type) {
		case string:
			return strings.TrimSpace(x)
		case float64:
			return fmt.Sprintf("%.0f", x)
		case json.Number:
			return x.String()
		}
	}
	return ""
}

func bytesValueToGB(s string) float64 {
	if s == "" {
		return 0
	}
	v := firstNumber(s)
	if v <= 0 {
		return 0
	}
	// rocm-smi and sysfs report bytes. Very small values are already GB.
	if v > 1e6 {
		return v / (1024 * 1024 * 1024)
	}
	return v
}

func (d SystemDetector) detectAMDviaSysfs(ctx context.Context, p *Profile) {
	drs, err := filepath.Glob("/sys/class/drm/card[0-9]*/device")
	if err != nil {
		return
	}
	for _, dir := range drs {
		vendorBytes, err := os.ReadFile(filepath.Join(dir, "vendor"))
		if err != nil || strings.TrimSpace(string(vendorBytes)) != "0x1002" {
			continue
		}
		totalBytes := readUintFile(filepath.Join(dir, "mem_info_vram_total"))
		usedBytes := readUintFile(filepath.Join(dir, "mem_info_vram_used"))
		if totalBytes == 0 {
			continue
		}
		slot := sysfsPCISlot(dir)
		name := lspciName(ctx, d, p, slot)
		if name == "" {
			name = "AMD Radeon GPU"
		}
		free := float64(totalBytes-usedBytes) / (1024 * 1024 * 1024)
		if free < 0 {
			free = 0
		}
		p.GPUs = append(p.GPUs, GPUInfo{
			Vendor: "AMD", Name: name, Kind: "discrete",
			VRAMTotalGB: float64(totalBytes) / (1024 * 1024 * 1024),
			VRAMFreeGB:  free,
			Source:      "amdgpu sysfs",
		})
		d.record(p, filepath.Join(dir, "mem_info_vram_total"), fmt.Sprintf("%d bytes", totalBytes))
	}
}

func readUintFile(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, _ := parseUint(strings.TrimSpace(string(b)))
	return v
}

func parseUint(s string) (uint64, error) {
	var v uint64
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number")
		}
		v = v*10 + uint64(r-'0')
	}
	return v, nil
}

func sysfsPCISlot(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "uevent"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PCI_SLOT_NAME="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func lspciName(ctx context.Context, d SystemDetector, p *Profile, slot string) string {
	if slot == "" {
		return ""
	}
	out, err := d.runCmd(ctx, p, "lspci", "-s", slot)
	if err != nil {
		return ""
	}
	if idx := strings.Index(out, ": "); idx >= 0 {
		return strings.TrimSpace(out[idx+2:])
	}
	return strings.TrimSpace(out)
}

func (d SystemDetector) detectLinuxGPUFallback(ctx context.Context, p *Profile) {
	out, err := d.runCmd(ctx, p, "lspci", "-mm")
	if err != nil {
		return
	}
	addLSPCIDevices(p, out)
}

func addLSPCIDevices(p *Profile, out string) {
	for _, line := range strings.Split(out, "\n") {
		dev, ok := parseLSPCIMM(line)
		if !ok {
			continue
		}
		class := strings.ToLower(dev.Class)
		if !strings.Contains(class, "vga") && !strings.Contains(class, "3d controller") && !strings.Contains(class, "display controller") {
			continue
		}
		vendor := gpu.CanonicalVendor(dev.Vendor + " " + dev.Device)
		if vendor == "unknown" {
			continue
		}
		if vendor == "NVIDIA" || vendor == "AMD" {
			if hasVendorGPU(p, vendor) {
				continue
			}
		}
		if hasSameGPUName(p, dev.Device) {
			continue
		}
		kind := "discrete"
		if vendor == "Intel" && !strings.Contains(strings.ToLower(dev.Device), "arc") {
			kind = "integrated"
		}
		g := GPUInfo{
			Vendor: vendor, Name: strings.TrimSpace(dev.Vendor + " " + dev.Device),
			Kind: kind, Source: "lspci",
		}
		if kind == "integrated" {
			g.Notes = append(g.Notes, "integrated GPU: shares system RAM and is treated as low-capability")
		} else {
			g.Notes = append(g.Notes, "vendor tool unavailable; VRAM/bandwidth estimated from lookup table")
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s GPU found via lspci but vendor tooling did not report it", vendor))
		}
		p.GPUs = append(p.GPUs, g)
	}
}

type lspciDevice struct {
	Slot   string
	Class  string
	Vendor string
	Device string
}

func parseLSPCIMM(line string) (lspciDevice, bool) {
	parts := strings.Split(line, "\"")
	if len(parts) < 6 {
		return lspciDevice{}, false
	}
	slotClass := strings.TrimSpace(parts[0])
	fields := strings.Fields(slotClass)
	slot := ""
	if len(fields) > 0 {
		slot = fields[0]
	}
	return lspciDevice{
		Slot: slot, Class: strings.TrimSpace(parts[1]),
		Vendor: strings.TrimSpace(parts[3]), Device: strings.TrimSpace(parts[5]),
	}, true
}
