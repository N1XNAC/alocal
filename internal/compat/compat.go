// Package compat turns real hardware and file-size metadata into an honest
// memory-fit classification for the model catalogue.
package compat

import (
	"fmt"
	"strings"

	"alocal/internal/hardware"
	"alocal/internal/scoring"
)

// Level is a compatibility classification.
type Level string

const (
	Recommended  Level = "Recommended"
	Warning      Level = "Warning"
	Incompatible Level = "Incompatible"
	Unknown      Level = "Unknown"
)

// SafetyMargin is reserved for the OS and inference backend, matching the
// recommendation engine's default.
const SafetyMargin = 0.15

// Result explains why a model was classified a certain way.
type Result struct {
	Level        Level                     `json:"level"`
	Need         scoring.ModelNeed         `json:"need"`
	Hardware     scoring.HardwareBreakdown `json:"hardware"`
	EffectiveGB  float64                   `json:"effective_memory_gb"`
	LimitGB      float64                   `json:"safe_limit_gb"`
	MemoryKind   string                    `json:"memory_kind"`
	MemoryDetail string                    `json:"memory_detail"`
	Reasons      []string                  `json:"reasons"`
}

// Check classifies a model against a detected hardware profile. It never
// claims a speed guarantee; parameter count is used only for demand context.
func Check(p *hardware.Profile, spec scoring.ModelSpec, contextSize int) Result {
	res := Result{Level: Unknown}
	if p == nil {
		res.Reasons = []string{"No hardware profile is available."}
		return res
	}
	if spec.FileSizeGB <= 0 {
		res.Reasons = []string{"This repository has no published GGUF file size, so memory fit cannot be checked."}
		return res
	}
	if contextSize <= 0 {
		contextSize = 8192
	}
	res.Need = scoring.ComputeModelNeed(spec, contextSize)
	res.Hardware = scoring.ScoreHardware(p)
	res.EffectiveGB, res.MemoryKind, res.MemoryDetail = scoring.EffectiveMemory(p)
	res.LimitGB = scoring.Round1(res.EffectiveGB * (1 - SafetyMargin))
	if res.EffectiveGB <= 0 {
		res.Reasons = []string{"Usable memory could not be determined from the hardware profile."}
		return res
	}

	res.Reasons = append(res.Reasons,
		fmt.Sprintf("Estimated need: %.1f GB (%.1f GB GGUF + %.1f GB KV cache, ×1.12 runtime overhead + 0.35 GB).",
			res.Need.RequiredGB, spec.FileSizeGB, res.Need.KVGB),
		fmt.Sprintf("Usable memory: %.1f GB %s (%s).", res.EffectiveGB, res.MemoryKind, res.MemoryDetail),
		fmt.Sprintf("Safe limit: %.1f GB after reserving %.0f%% for the OS and backend.", res.LimitGB, SafetyMargin*100),
	)
	if res.Need.KVUnknown {
		res.Reasons = append(res.Reasons, "Layer/head metadata is not published, so KV-cache size could not be estimated; leave extra headroom.")
	}
	if res.Need.ContextCapped {
		res.Reasons = append(res.Reasons, fmt.Sprintf("Requested context was capped to the model's %d-token maximum.", res.Need.Context))
	}
	res.Reasons = append(res.Reasons, backendNote(p))

	switch {
	case res.Need.RequiredGB > res.EffectiveGB:
		res.Level = Incompatible
		res.Reasons = append(res.Reasons, fmt.Sprintf("Estimated need exceeds usable memory by %.1f GB.", res.Need.RequiredGB-res.EffectiveGB))
	case res.Need.RequiredGB > res.LimitGB:
		res.Level = Warning
		res.Reasons = append(res.Reasons, "The model may fit, but there is less than the recommended safety headroom.")
	case res.Need.KVUnknown:
		if res.Need.RequiredGB <= res.LimitGB*0.7 {
			res.Level = Recommended
		} else {
			res.Level = Warning
		}
	default:
		res.Level = Recommended
	}
	res.Reasons = append(res.Reasons, "This is a memory-fit estimate, not a speed guarantee; parameter count alone does not predict performance.")
	return res
}

func backendNote(p *hardware.Profile) string {
	if gpus := p.DiscreteGPUs(); len(gpus) > 0 {
		names := make([]string, 0, len(gpus))
		for _, g := range gpus {
			names = append(names, strings.TrimSpace(g.Name))
		}
		return "Acceleration: discrete GPU (" + strings.Join(names, ", ") + ") via llama.cpp."
	}
	if _, ok := p.UnifiedGPU(); ok {
		return "Acceleration: Apple/unified memory via llama.cpp Metal."
	}
	if len(p.CPU.SIMD) > 0 {
		return "Acceleration: CPU inference with " + strings.Join(p.CPU.SIMD, "/") + "."
	}
	return "Acceleration: CPU inference; no SIMD capabilities were detected."
}
