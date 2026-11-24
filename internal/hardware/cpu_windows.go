//go:build windows

package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"syscall"
)

func (d SystemDetector) enrichCPU(ctx context.Context, p *Profile) {
	out, err := d.runCmd(ctx, p, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance Win32_Processor | Select-Object Name,MaxClockSpeed,CurrentClockSpeed,NumberOfCores,NumberOfLogicalProcessors | ConvertTo-Json -Compress")
	if err == nil {
		for _, obj := range decodePowerShellObjects(out) {
			if name := stringValue(obj["Name"]); name != "" && (p.CPU.Model == "" || p.CPU.Model == "unknown CPU") {
				p.CPU.Model = name
			}
			max := numberValue(obj["MaxClockSpeed"])
			cur := numberValue(obj["CurrentClockSpeed"])
			if max > 0 {
				p.CPU.MaxMHz = max
			}
			if cur > 0 {
				p.CPU.BaseMHz = cur
			}
			if cores := int(numberValue(obj["NumberOfCores"])); cores > 0 {
				p.CPU.PhysicalCores = cores
			}
			if threads := int(numberValue(obj["NumberOfLogicalProcessors"])); threads > 0 {
				p.CPU.LogicalThreads = threads
			}
			break
		}
	}
	if p.CPU.MaxMHz == 0 && p.CPU.BaseMHz > 0 {
		p.CPU.MaxMHz = p.CPU.BaseMHz
	}
	if p.CPU.BaseMHz == 0 && p.CPU.MaxMHz > 0 {
		p.CPU.BaseMHz = p.CPU.MaxMHz
	}

	if runtime.GOARCH == "arm64" {
		p.CPU.SIMD = append(p.CPU.SIMD, "NEON")
		return
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("IsProcessorFeaturePresent")
	present := func(feature uintptr) bool {
		r, _, _ := proc.Call(feature)
		return r != 0
	}
	var simd []string
	if present(41) { // PF_AVX512F_INSTRUCTIONS_AVAILABLE
		simd = append(simd, "AVX512")
	}
	if present(40) { // PF_AVX2_INSTRUCTIONS_AVAILABLE
		simd = append(simd, "AVX2")
	}
	if present(38) { // PF_SSE4_2_INSTRUCTIONS_AVAILABLE
		simd = append(simd, "SSE4.2")
	}
	if len(simd) == 0 {
		p.CPU.SIMDNote = "no AVX2/AVX512 feature reported by IsProcessorFeaturePresent"
	}
	p.CPU.SIMD = simd
}

func (d SystemDetector) enrichRAM(ctx context.Context, p *Profile) {
	out, err := d.runCmd(ctx, p, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance Win32_PhysicalMemory | Select-Object Capacity,Speed,ConfiguredClockSpeed,SMBIOSMemoryType | ConvertTo-Json -Compress")
	if err != nil {
		return
	}
	objs := decodePowerShellObjects(out)
	var total uint64
	for _, obj := range objs {
		total += uint64(numberValue(obj["Capacity"]))
		if p.RAM.SpeedMTs == 0 {
			s := int(numberValue(obj["ConfiguredClockSpeed"]))
			if s == 0 {
				s = int(numberValue(obj["Speed"]))
			}
			p.RAM.SpeedMTs = s
		}
		if p.RAM.Type == "" {
			p.RAM.Type = smbiosMemoryType(int(numberValue(obj["SMBIOSMemoryType"])))
		}
	}
	if p.RAM.TotalGB == 0 && total > 0 {
		p.RAM.TotalGB = float64(total) / (1024 * 1024 * 1024)
		p.RAM.AvailableGB = p.RAM.TotalGB
	}
	if p.RAM.Channels == 0 && len(objs) > 0 {
		p.RAM.Channels = len(objs)
	}
}

func (d SystemDetector) detectPlatformGPUs(ctx context.Context, p *Profile) {
	d.detectWindowsGPUs(ctx, p)
}

func (d SystemDetector) detectWindowsGPUs(ctx context.Context, p *Profile) {
	out, err := d.runCmd(ctx, p, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance Win32_VideoController | Select-Object Name,AdapterRAM,DriverVersion,AdapterCompatibility | ConvertTo-Json -Compress")
	if err != nil {
		return
	}
	for _, obj := range decodePowerShellObjects(out) {
		name := stringValue(obj["Name"])
		if name == "" {
			continue
		}
		vendor := gpuVendorFromName(name + " " + stringValue(obj["AdapterCompatibility"]))
		if vendor == "NVIDIA" && hasVendorGPU(p, "NVIDIA") {
			continue
		}
		if hasSameGPUName(p, name) {
			continue
		}
		kind := "discrete"
		lower := strings.ToLower(name)
		if vendor == "Intel" && !strings.Contains(lower, "arc") {
			kind = "integrated"
		}
		vram := float64(numberValue(obj["AdapterRAM"])) / (1024 * 1024 * 1024)
		notes := []string{}
		// Win32_VideoController.AdapterRAM is a uint32 and wraps at 4 GiB.
		if vram >= 4.0-0.02 {
			vram = 0
			notes = append(notes, "WMI VRAM is unreliable (uint32 cap); value taken from lookup table if available")
		}
		p.GPUs = append(p.GPUs, GPUInfo{
			Vendor: vendor, Name: name, Kind: kind,
			VRAMTotalGB: vram, Driver: stringValue(obj["DriverVersion"]),
			Source: "Win32_VideoController", Notes: notes,
		})
	}
}

func (d SystemDetector) detectDiskKind(ctx context.Context, p *Profile, device, mount string) string {
	out, err := d.runCmd(ctx, p, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Get-PhysicalDisk | Select-Object FriendlyName,MediaType,BusType,Size | ConvertTo-Json -Compress")
	if err != nil {
		return "unknown"
	}
	for _, obj := range decodePowerShellObjects(out) {
		bus := strings.ToLower(stringValue(obj["BusType"]))
		media := strings.ToLower(stringValue(obj["MediaType"]))
		if strings.Contains(bus, "nvme") {
			return "nvme"
		}
		if strings.Contains(media, "ssd") {
			return "ssd"
		}
		if strings.Contains(media, "hdd") || strings.Contains(media, "hard") {
			return "hdd"
		}
	}
	return "unknown"
}

func decodePowerShellObjects(out string) []map[string]any {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err == nil {
		return arr
	}
	var one map[string]any
	if err := json.Unmarshal([]byte(out), &one); err == nil {
		return []map[string]any{one}
	}
	return nil
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return fmt.Sprintf("%.0f", x)
	}
	return ""
}

func numberValue(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		v := firstNumber(x)
		return v
	}
	return 0
}

func smbiosMemoryType(code int) string {
	switch code {
	case 24:
		return "DDR3"
	case 26:
		return "DDR4"
	case 27, 28, 29:
		return "LPDDR3"
	case 30:
		return "LPDDR4"
	case 34:
		return "DDR5"
	case 35:
		return "LPDDR5"
	}
	return ""
}
