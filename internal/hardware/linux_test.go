//go:build linux

package hardware

import (
	"math"
	"testing"
)

func TestParseROCMJSON(t *testing.T) {
	out := `{
	  "card0": {
	    "Card series": "AMD Radeon RX 7900 XTX",
	    "VRAM Total Memory (B)": "25753026560",
	    "VRAM Total Used Memory (B)": "1073741824",
	    "Driver version": "6.10.5"
	  },
	  "system": {"some": "value"}
	}`
	p := &Profile{}
	n := parseROCMJSON(p, out)
	if n != 1 {
		t.Fatalf("parsed %d cards, want 1", n)
	}
	g := p.GPUs[0]
	if g.Name != "AMD Radeon RX 7900 XTX" || g.Vendor != "AMD" {
		t.Errorf("bad name/vendor: %+v", g)
	}
	if math.Abs(g.VRAMTotalGB-23.98) > 0.05 {
		t.Errorf("VRAM = %.2f", g.VRAMTotalGB)
	}
	if math.Abs(g.VRAMFreeGB-22.98) > 0.05 {
		t.Errorf("free VRAM = %.2f", g.VRAMFreeGB)
	}
	if g.Driver != "6.10.5" {
		t.Errorf("driver = %q", g.Driver)
	}
}

func TestParseLSPCIMM(t *testing.T) {
	line := `0000:01:00.0 "VGA compatible controller" "NVIDIA Corporation" "GA104 [GeForce RTX 3060]" -r00`
	dev, ok := parseLSPCIMM(line)
	if !ok {
		t.Fatal("parse failed")
	}
	if dev.Slot != "0000:01:00.0" || dev.Class != "VGA compatible controller" ||
		dev.Vendor != "NVIDIA Corporation" || dev.Device != "GA104 [GeForce RTX 3060]" {
		t.Errorf("unexpected parse: %+v", dev)
	}
	if _, ok := parseLSPCIMM("garbage"); ok {
		t.Error("garbage parsed")
	}
}

func TestParseDMIOutput(t *testing.T) {
	out := `
Memory Device
	Size: 16 GB
	Type: DDR4
	Speed: 3200 MT/s
	Configured Memory Speed: 2933 MT/s
`
	memType, speed := parseDMIOutput(out)
	if memType != "DDR4" || speed != 3200 {
		t.Errorf("got %q %d", memType, speed)
	}
}

func TestLinuxBaseDevice(t *testing.T) {
	cases := map[string]string{
		"/dev/sda10":     "sda",
		"/dev/nvme0n1p2": "nvme0n1",
		"/dev/mmcblk0p1": "mmcblk0",
		"/dev/vdb2":      "vdb",
		"/dev/xvda1":     "xvda",
	}
	for in, want := range cases {
		if got := linuxBaseDevice(in); got != want {
			t.Errorf("linuxBaseDevice(%q) = %q, want %q", in, got, want)
		}
	}
}
