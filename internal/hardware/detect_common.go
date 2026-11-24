package hardware

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// Detect probes the live machine. It returns a profile even when individual
// probes fail; failures are recorded as warnings/sources.
func (d SystemDetector) Detect(ctx context.Context) (*Profile, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p := &Profile{DetectedAt: time.Now().UTC()}
	d.detectOS(p)
	d.detectCPU(ctx, p)
	d.enrichCPU(ctx, p)
	d.detectRAM(ctx, p)
	d.enrichRAM(ctx, p)
	d.detectGPUs(ctx, p)
	d.detectDisk(ctx, p)
	if err := p.Normalize(); err != nil {
		p.Warnings = append(p.Warnings, "incomplete hardware profile: "+err.Error())
	}
	return p, nil
}

func (d SystemDetector) timeout() time.Duration {
	if d.CommandTimeout > 0 {
		return d.CommandTimeout
	}
	return 8 * time.Second
}

func (d SystemDetector) runCmd(ctx context.Context, p *Profile, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	src := Source{Name: name, Command: strings.Join(append([]string{name}, args...), " "), Output: truncate(text, 12000)}
	if err != nil {
		src.Error = err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			src.Error = "timed out after " + d.timeout().String()
		}
	}
	p.Sources = append(p.Sources, src)
	return text, err
}

func (d SystemDetector) record(p *Profile, name, output string) {
	if p == nil {
		return
	}
	p.Sources = append(p.Sources, Source{Name: name, Output: truncate(output, 12000)})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

func (d SystemDetector) detectOS(p *Profile) {
	p.OS = OSInfo{Name: runtime.GOOS, Arch: archName(runtime.GOARCH)}
	info, err := host.Info()
	if err == nil {
		if info.Platform != "" {
			p.OS.Name = info.Platform
		}
		p.OS.Version = strings.TrimSpace(info.PlatformVersion)
		p.OS.Kernel = info.KernelVersion
		d.record(p, "gopsutil host.Info", fmt.Sprintf("platform=%s version=%s kernel=%s virtualization=%s/%s",
			info.Platform, info.PlatformVersion, info.KernelVersion, info.VirtualizationSystem, info.VirtualizationRole))
	} else {
		d.record(p, "gopsutil host.Info", "error: "+err.Error())
	}
	p.OS.Container, p.OS.WSL = detectEnvironment()
	if p.OS.Container {
		p.Warnings = append(p.Warnings, "running inside a container; CPU/RAM limits may come from the host")
	}
	if p.OS.WSL {
		p.Warnings = append(p.Warnings, "running under WSL; GPU passthrough depends on the Windows driver")
	}
}

func detectEnvironment() (container, wsl bool) {
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			container = true
		}
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := strings.ToLower(string(b))
		for _, marker := range []string{"docker", "kubepods", "containerd", "lxc", "podman"} {
			if strings.Contains(s, marker) {
				container = true
			}
		}
	}
	if strings.TrimSpace(os.Getenv("container")) != "" || os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		container = true
	}
	if strings.TrimSpace(os.Getenv("DOTNET_RUNNING_IN_CONTAINER")) == "true" {
		container = true
	}
	if b, err := os.ReadFile("/proc/version"); err == nil {
		if strings.Contains(strings.ToLower(string(b)), "microsoft") {
			wsl = true
		}
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		wsl = true
	}
	return container, wsl
}

func archName(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "x86"
	case "arm":
		return "arm"
	default:
		return goarch
	}
}

func (d SystemDetector) detectCPU(ctx context.Context, p *Profile) {
	infos, err := cpu.InfoWithContext(ctx)
	if err != nil {
		d.record(p, "gopsutil cpu.Info", "error: "+err.Error())
		p.Warnings = append(p.Warnings, "CPU information unavailable: "+err.Error())
	}
	if len(infos) > 0 {
		info := infos[0]
		p.CPU.Model = strings.TrimSpace(info.ModelName)
		p.CPU.Vendor = strings.TrimSpace(info.VendorID)
		if info.Mhz > 0 {
			p.CPU.BaseMHz = info.Mhz
			p.CPU.MaxMHz = info.Mhz
		}
		d.record(p, "gopsutil cpu.Info", fmt.Sprintf("%d entries; model=%q mhz=%.0f cores=%d",
			len(infos), info.ModelName, info.Mhz, info.Cores))
	}
	physical, err := cpu.CountsWithContext(ctx, false)
	if err != nil {
		d.record(p, "gopsutil cpu.Counts(physical)", "error: "+err.Error())
	}
	logical, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		d.record(p, "gopsutil cpu.Counts(logical)", "error: "+err.Error())
	}
	p.CPU.PhysicalCores = physical
	p.CPU.LogicalThreads = logical
	if p.CPU.LogicalThreads == 0 {
		p.CPU.LogicalThreads = runtime.NumCPU()
	}
	if p.CPU.PhysicalCores == 0 {
		p.CPU.PhysicalCores = p.CPU.LogicalThreads
	}
	if p.CPU.Model == "" {
		p.CPU.Model = "unknown CPU"
		p.Warnings = append(p.Warnings, "CPU model name could not be detected")
	}
	p.CPU.Arch = archName(runtime.GOARCH)
}

func (d SystemDetector) detectRAM(ctx context.Context, p *Profile) {
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		d.record(p, "gopsutil mem.VirtualMemory", "error: "+err.Error())
		p.Warnings = append(p.Warnings, "RAM information unavailable: "+err.Error())
		return
	}
	p.RAM.TotalGB = bytesToGB(vm.Total)
	p.RAM.AvailableGB = bytesToGB(vm.Available)
	d.record(p, "gopsutil mem.VirtualMemory", fmt.Sprintf("total=%d available=%d", vm.Total, vm.Available))
}

func (d SystemDetector) detectGPUs(ctx context.Context, p *Profile) {
	d.detectNVIDIA(ctx, p)
	d.detectPlatformGPUs(ctx, p)
}

func (d SystemDetector) detectNVIDIA(ctx context.Context, p *Profile) {
	out, err := d.runCmd(ctx, p, "nvidia-smi",
		"--query-gpu=name,memory.total,memory.free,driver_version",
		"--format=csv,noheader,nounits")
	if err != nil {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if strings.EqualFold(name, "name") {
			continue
		}
		total := parseMiB(fields[1])
		free := parseMiB(fields[2])
		driver := ""
		if len(fields) > 3 {
			driver = strings.TrimSpace(fields[3])
		}
		if total <= 0 {
			continue
		}
		p.GPUs = append(p.GPUs, GPUInfo{
			Vendor: "NVIDIA", Name: name, Kind: "discrete",
			VRAMTotalGB: total, VRAMFreeGB: free, Driver: driver,
			Source: "nvidia-smi",
		})
	}
}

func parseMiB(s string) float64 {
	v := firstNumber(s)
	if v <= 0 {
		return 0
	}
	// nvidia-smi is queried in MiB; convert to GiB.
	return v / 1024.0
}

func firstNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "N/A") {
		return 0
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimRight(fields[0], "iB,;"), 64)
	if err != nil {
		return 0
	}
	return v
}

func bytesToGB(b uint64) float64 { return float64(b) / (1024 * 1024 * 1024) }

func (d SystemDetector) detectDisk(ctx context.Context, p *Profile) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "/"
	}
	partitions, _ := disk.PartitionsWithContext(ctx, false)
	bestMount := ""
	device := ""
	for _, part := range partitions {
		m := part.Mountpoint
		if m == "" {
			continue
		}
		cleanHome := filepath.Clean(home)
		cleanMount := filepath.Clean(m)
		if cleanHome == cleanMount || strings.HasPrefix(cleanHome, cleanMount+string(os.PathSeparator)) || cleanMount == "/" || (runtime.GOOS == "windows" && strings.EqualFold(filepath.VolumeName(cleanHome), strings.TrimRight(cleanMount, "\\"))) {
			if len(cleanMount) > len(bestMount) {
				bestMount = cleanMount
				device = part.Device
			}
		}
	}
	usagePath := home
	if bestMount != "" {
		usagePath = bestMount
	}
	usage, err := disk.UsageWithContext(ctx, usagePath)
	if err != nil {
		d.record(p, "gopsutil disk.Usage", "error: "+err.Error())
		p.Warnings = append(p.Warnings, "disk information unavailable: "+err.Error())
		return
	}
	p.Disk = DiskInfo{
		Mount: bestMount, Device: device,
		TotalGB: bytesToGB(usage.Total), FreeGB: bytesToGB(usage.Free),
	}
	d.record(p, "gopsutil disk.Usage", fmt.Sprintf("path=%s total=%d free=%d device=%s", usagePath, usage.Total, usage.Free, device))
	p.Disk.Kind = d.detectDiskKind(ctx, p, device, bestMount)
}

const (
	GB = 1024 * 1024 * 1024
)
