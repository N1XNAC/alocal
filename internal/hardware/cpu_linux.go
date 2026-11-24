//go:build linux

package hardware

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (d SystemDetector) enrichCPU(ctx context.Context, p *Profile) {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		d.record(p, "/proc/cpuinfo", "error: "+err.Error())
		return
	}
	text := string(data)
	d.record(p, "/proc/cpuinfo", text)

	flags := map[string]bool{}
	var model, vendor string
	var currentMHz float64
	for _, line := range strings.Split(text, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		switch key {
		case "model name", "processor":
			if model == "" {
				model = val
			}
		case "vendor_id":
			if vendor == "" {
				vendor = val
			}
		case "cpu mhz":
			if currentMHz == 0 {
				currentMHz, _ = strconv.ParseFloat(val, 64)
			}
		case "flags":
			for _, f := range strings.Fields(val) {
				flags[strings.ToLower(f)] = true
			}
		case "features":
			for _, f := range strings.Fields(val) {
				flags[strings.ToLower(f)] = true
			}
		}
	}
	if p.CPU.Model == "" || p.CPU.Model == "unknown CPU" {
		p.CPU.Model = model
	}
	if p.CPU.Vendor == "" {
		p.CPU.Vendor = vendor
	}
	if p.CPU.BaseMHz == 0 {
		p.CPU.BaseMHz = currentMHz
	}
	if p.CPU.MaxMHz == 0 {
		p.CPU.MaxMHz = currentMHz
	}
	for _, cpu := range []string{"cpu0", "cpu1"} {
		base := readFloatFile(filepath.Join("/sys/devices/system/cpu", cpu, "cpufreq/base_frequency"))
		max := readFloatFile(filepath.Join("/sys/devices/system/cpu", cpu, "cpufreq/cpuinfo_max_freq"))
		if max == 0 {
			max = readFloatFile(filepath.Join("/sys/devices/system/cpu", cpu, "cpufreq/scaling_max_freq"))
		}
		if base > 0 {
			p.CPU.BaseMHz = base / 1000
		}
		if max > 0 {
			p.CPU.MaxMHz = max / 1000
		}
		if p.CPU.BaseMHz > 0 && p.CPU.MaxMHz > 0 {
			break
		}
	}
	var simd []string
	if flags["avx512f"] {
		simd = append(simd, "AVX512")
	}
	if flags["avx2"] {
		simd = append(simd, "AVX2")
	}
	if flags["f16c"] {
		simd = append(simd, "F16C")
	}
	if flags["sse4_2"] {
		simd = append(simd, "SSE4.2")
	}
	if flags["asimd"] || flags["neon"] {
		simd = append(simd, "NEON")
	}
	if len(simd) == 0 {
		p.CPU.SIMDNote = "no AVX2/AVX512/NEON flag found in /proc/cpuinfo"
	}
	p.CPU.SIMD = simd
}

func (d SystemDetector) enrichRAM(ctx context.Context, p *Profile) {
	out, err := d.runCmd(ctx, p, "dmidecode", "-t", "memory")
	if err == nil {
		memType, speed := parseDMIOutput(out)
		if memType != "" && !strings.EqualFold(memType, "unknown") && !strings.EqualFold(memType, "other") {
			p.RAM.Type = memType
		}
		if speed > 0 {
			p.RAM.SpeedMTs = speed
		}
		if p.RAM.Type != "" {
			return
		}
	}
	out, err = d.runCmd(ctx, p, "lshw", "-class", "memory")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			lower := strings.ToLower(line)
			if strings.Contains(lower, "description:") && p.RAM.Type == "" {
				for _, t := range []string{"DDR5", "DDR4", "DDR3", "LPDDR5", "LPDDR4"} {
					if strings.Contains(strings.ToUpper(line), t) {
						p.RAM.Type = t
					}
				}
			}
			if strings.Contains(lower, "clock:") && p.RAM.SpeedMTs == 0 {
				v := firstNumber(strings.TrimPrefix(line, "clock:"))
				if v > 0 && v < 20000 {
					p.RAM.SpeedMTs = int(v)
				}
			}
		}
	}
}

func parseDMIOutput(out string) (string, int) {
	var memType string
	var speed int
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "type:") && memType == "" {
			v := strings.TrimSpace(strings.TrimPrefix(line, "Type:"))
			v = strings.TrimSpace(strings.TrimPrefix(v, "type:"))
			if v != "" && !strings.EqualFold(v, "unknown") {
				memType = v
			}
		}
		if (strings.HasPrefix(lower, "speed:") || strings.HasPrefix(lower, "configured memory speed:")) && speed == 0 {
			idx := strings.Index(line, ":")
			if idx >= 0 {
				v := firstNumber(line[idx+1:])
				if v > 0 {
					speed = int(v)
				}
			}
		}
	}
	return memType, speed
}

func readFloatFile(path string) float64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	return v
}

func (d SystemDetector) detectDiskKind(ctx context.Context, p *Profile, device, mount string) string {
	dev := strings.TrimSpace(device)
	if dev == "" {
		return "unknown"
	}
	base := linuxBaseDevice(dev)
	if base == "" {
		return "unknown"
	}
	rotPath := filepath.Join("/sys/block", base, "queue/rotational")
	b, err := os.ReadFile(rotPath)
	if err != nil {
		return "unknown"
	}
	rot := strings.TrimSpace(string(b))
	d.record(p, rotPath, rot)
	if rot == "1" {
		return "hdd"
	}
	if strings.HasPrefix(base, "nvme") {
		return "nvme"
	}
	return "ssd"
}

func linuxBaseDevice(dev string) string {
	dev = strings.TrimPrefix(dev, "/dev/")
	if strings.HasPrefix(dev, "mapper/") {
		if target, err := os.Readlink("/dev/" + dev); err == nil {
			dev = strings.TrimPrefix(target, "/dev/")
		}
	}
	if strings.HasPrefix(dev, "dm-") {
		slaves, _ := filepath.Glob("/sys/block/" + dev + "/slaves/*")
		if len(slaves) > 0 {
			return filepath.Base(slaves[0])
		}
		return dev
	}
	if strings.HasPrefix(dev, "nvme") || strings.HasPrefix(dev, "mmcblk") {
		if idx := strings.LastIndex(dev, "p"); idx > 0 {
			if _, err := strconv.Atoi(dev[idx+1:]); err == nil {
				return dev[:idx]
			}
		}
		return dev
	}
	// sda1 -> sda, vdb2 -> vdb, xvda1 -> xvda.
	i := len(dev)
	for i > 0 && dev[i-1] >= '0' && dev[i-1] <= '9' {
		i--
	}
	if i > 0 {
		return dev[:i]
	}
	return dev
}
