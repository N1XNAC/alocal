//go:build darwin

package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (d SystemDetector) enrichCPU(ctx context.Context, p *Profile) {
	if out, err := d.runCmd(ctx, p, "sysctl", "-n", "machdep.cpu.brand_string"); err == nil && out != "" {
		p.CPU.Model = strings.TrimSpace(out)
	}
	if out, err := d.runCmd(ctx, p, "sysctl", "-n", "hw.cpufrequency"); err == nil {
		if v, _ := strconv.ParseFloat(strings.TrimSpace(out), 64); v > 0 {
			p.CPU.BaseMHz = v / 1e6
		}
	}
	if out, err := d.runCmd(ctx, p, "sysctl", "-n", "hw.cpufrequency_max"); err == nil {
		if v, _ := strconv.ParseFloat(strings.TrimSpace(out), 64); v > 0 {
			p.CPU.MaxMHz = v / 1e6
		}
	}
	if out, err := d.runCmd(ctx, p, "sysctl", "-n", "hw.memsize"); err == nil {
		if v, _ := strconv.ParseUint(strings.TrimSpace(out), 10, 64); v > 0 && p.RAM.TotalGB == 0 {
			p.RAM.TotalGB = float64(v) / (1024 * 1024 * 1024)
			p.RAM.AvailableGB = p.RAM.TotalGB
		}
	}

	var simd []string
	if isAppleSilicon(p.CPU.Model) || strings.HasPrefix(strings.ToLower(p.CPU.Arch), "aarch64") {
		simd = append(simd, "NEON")
	} else {
		out, err := d.runCmd(ctx, p, "sysctl", "-n", "machdep.cpu.leaf7_features")
		if err == nil {
			if strings.Contains(strings.ToUpper(out), "AVX512") {
				simd = append(simd, "AVX512")
			}
			if strings.Contains(strings.ToUpper(out), "AVX2") {
				simd = append(simd, "AVX2")
			}
		}
		out, err = d.runCmd(ctx, p, "sysctl", "-n", "machdep.cpu.features")
		if err == nil {
			if strings.Contains(strings.ToUpper(out), "SSE4.2") {
				simd = append(simd, "SSE4.2")
			}
			if strings.Contains(strings.ToUpper(out), "F16C") {
				simd = append(simd, "F16C")
			}
		}
	}
	if len(simd) == 0 {
		p.CPU.SIMDNote = "SIMD flags unavailable from sysctl"
	}
	p.CPU.SIMD = simd
}

func (d SystemDetector) enrichRAM(ctx context.Context, p *Profile) {
	out, err := d.runCmd(ctx, p, "system_profiler", "SPMemoryDataType")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Type:") && p.RAM.Type == "" {
				p.RAM.Type = strings.TrimSpace(strings.TrimPrefix(line, "Type:"))
			}
			if strings.HasPrefix(line, "Speed:") && p.RAM.SpeedMTs == 0 {
				p.RAM.SpeedMTs = int(firstNumber(strings.TrimPrefix(line, "Speed:")))
			}
		}
	}
	if p.RAM.Type == "" && isAppleSilicon(p.CPU.Model) {
		switch {
		case strings.Contains(p.CPU.Model, "M4"):
			p.RAM.Type, p.RAM.SpeedMTs = "LPDDR5X", 7500
		case strings.Contains(p.CPU.Model, "M3"):
			p.RAM.Type, p.RAM.SpeedMTs = "LPDDR5X", 7500
		case strings.Contains(p.CPU.Model, "M2"):
			p.RAM.Type, p.RAM.SpeedMTs = "LPDDR5", 6400
		case strings.Contains(p.CPU.Model, "M1"):
			p.RAM.Type, p.RAM.SpeedMTs = "LPDDR4X", 4266
		}
	}
	if p.RAM.Channels == 0 && isAppleSilicon(p.CPU.Model) {
		p.RAM.Channels = 2
	}
}

func isAppleSilicon(model string) bool {
	return strings.Contains(strings.ToLower(model), "apple m")
}

func (d SystemDetector) detectPlatformGPUs(ctx context.Context, p *Profile) {
	d.detectAppleGPUs(ctx, p)
}

func (d SystemDetector) detectAppleGPUs(ctx context.Context, p *Profile) {
	if isAppleSilicon(p.CPU.Model) {
		name := strings.TrimSpace(p.CPU.Model)
		if name == "" {
			name = "Apple Silicon"
		}
		p.GPUs = append(p.GPUs, GPUInfo{
			Vendor: "Apple", Name: name, Kind: "unified",
			VRAMTotalGB: p.RAM.TotalGB, VRAMFreeGB: p.RAM.AvailableGB,
			Source: "sysctl",
			Notes:  []string{"unified memory: scoring treats 70% of RAM as GPU-usable"},
		})
	}
	out, err := d.runCmd(ctx, p, "system_profiler", "SPDisplaysDataType", "-json")
	if err != nil {
		return
	}
	var root struct {
		Displays []map[string]any `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		return
	}
	for _, disp := range root.Displays {
		name := stringField(disp, "sppci_model")
		if name == "" {
			name = stringField(disp, "spdisplays_chipset_model")
		}
		if name == "" || hasSameGPUName(p, name) {
			continue
		}
		vendor := gpuVendorFromName(name + " " + stringField(disp, "sppci_vendor"))
		kind := "discrete"
		shared := strings.EqualFold(strings.TrimSpace(stringField(disp, "spdisplays_vram_shared")), "yes")
		vram := parseGBString(stringField(disp, "spdisplays_vram"))
		if shared || vram == 0 {
			kind = "integrated"
		}
		if isAppleSilicon(p.CPU.Model) && strings.Contains(strings.ToLower(name), "apple") {
			kind = "unified"
			vram = p.RAM.TotalGB
		}
		p.GPUs = append(p.GPUs, GPUInfo{
			Vendor: vendor, Name: name, Kind: kind,
			VRAMTotalGB: vram, Source: "system_profiler",
		})
	}
	if len(p.GPUs) == 0 {
		p.Warnings = append(p.Warnings, "no GPU detected; treating this as a CPU-only machine")
	}
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case string:
			return strings.TrimSpace(x)
		case float64:
			return fmt.Sprintf("%.0f", x)
		}
	}
	return ""
}

func parseGBString(s string) float64 {
	v := firstNumber(s)
	if v <= 0 {
		return 0
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "mb") && !strings.Contains(lower, "gb") {
		return v / 1024
	}
	return v
}

func (d SystemDetector) detectDiskKind(ctx context.Context, p *Profile, device, mount string) string {
	target := device
	if target == "" {
		target = mount
	}
	if target == "" {
		target = "/"
	}
	out, err := d.runCmd(ctx, p, "diskutil", "info", target)
	if err != nil {
		return "unknown"
	}
	solid := false
	nvme := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "solid state:") && strings.Contains(lower, "yes") {
			solid = true
		}
		if strings.Contains(lower, "protocol:") && (strings.Contains(lower, "nvme") || strings.Contains(lower, "apple fabric")) {
			nvme = true
		}
		if strings.Contains(lower, "device / media name:") && strings.Contains(lower, "nvme") {
			nvme = true
		}
	}
	if nvme {
		return "nvme"
	}
	if solid {
		return "ssd"
	}
	return "hdd"
}
