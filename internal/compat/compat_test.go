package compat

import (
	"strings"
	"testing"

	"alocal/internal/hardware"
	"alocal/internal/scoring"
)

func profile(t *testing.T, name string) *hardware.Profile {
	t.Helper()
	p, err := hardware.LoadProfile("../../tests/fixtures/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckBigModelOnSmallMachineIsIncompatible(t *testing.T) {
	spec := scoring.ModelSpec{
		ID: "test/70b", ParamsB: 70, Quant: "Q4_K_M", FileSizeGB: 40,
		MaxContext: 8192, Layers: 80, KVHeads: 8, HeadDim: 128,
	}
	got := Check(profile(t, "nogpu4"), spec, 8192)
	if got.Level != Incompatible {
		t.Fatalf("level = %s, want Incompatible: %+v", got.Level, got)
	}
	if !strings.Contains(strings.Join(got.Reasons, " "), "exceeds usable memory") {
		t.Fatalf("missing explanation: %v", got.Reasons)
	}
}

func TestCheckSmallModelOnLargeMachineIsRecommended(t *testing.T) {
	spec := scoring.ModelSpec{
		ID: "test/7b", ParamsB: 7.6, Quant: "Q4_K_M", FileSizeGB: 4.7,
		MaxContext: 8192, Layers: 28, KVHeads: 4, HeadDim: 128,
	}
	got := Check(profile(t, "rtx4090"), spec, 8192)
	if got.Level != Recommended {
		t.Fatalf("level = %s, want Recommended: %+v", got.Level, got)
	}
	if !strings.Contains(strings.Join(got.Reasons, " "), "not a speed guarantee") {
		t.Fatalf("missing no-guarantee note: %v", got.Reasons)
	}
}

func TestCheckUnknownFileSizeIsUnknown(t *testing.T) {
	got := Check(profile(t, "rtx3060"), scoring.ModelSpec{ID: "x", ParamsB: 7}, 8192)
	if got.Level != Unknown {
		t.Fatalf("level = %s, want Unknown", got.Level)
	}
}
