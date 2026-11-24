package scoring

import (
	"fmt"

	"alocal/internal/gpu"
	"alocal/internal/hardware"
)

// ModelSpec is the subset of a catalog entry needed to score memory and
// throughput. Keeping it separate avoids an import cycle with internal/catalog.
type ModelSpec struct {
	ID            string
	ParamsB       float64
	ActiveParamsB float64
	Quant         string
	FileSizeGB    float64
	MaxContext    int
	Layers        int
	KVHeads       int
	HeadDim       int
}

// ModelNeed explains a model's hardware requirement.
type ModelNeed struct {
	Context          int     `json:"context"`
	RequestedContext int     `json:"requested_context"`
	ContextCapped    bool    `json:"context_capped"`
	KVGB             float64 `json:"kv_cache_gb"`
	KVUnknown        bool    `json:"kv_unknown"`
	ActiveWeightGB   float64 `json:"active_weight_gb"`
	RequiredGB       float64 `json:"required_memory_gb"`
	MemoryScore      float64 `json:"memory_score"`
	DemandScore      float64 `json:"demand_score"`
	Score            float64 `json:"model_score"`
	BitsPerWeight    float64 `json:"bits_per_weight"`
}

// ComputeModelNeed applies the documented model-score formula:
//
//	KV bytes/token = 2 (K+V) × layers × kv_heads × head_dim × 2 bytes
//	required GB    = (file_size + KV cache) × 1.12 + 0.35
//	model score    = 0.70 × memory_need_curve(required)
//	               + 0.30 × demand_curve(active_params × bits_per_weight / 8)
func ComputeModelNeed(spec ModelSpec, context int) ModelNeed {
	if context <= 0 {
		context = 8192
	}
	need := ModelNeed{RequestedContext: context}
	if spec.MaxContext > 0 && context > spec.MaxContext {
		context = spec.MaxContext
		need.ContextCapped = true
	}
	need.Context = context

	bpw, ok := BitsPerWeight[spec.Quant]
	if !ok {
		bpw = 4.85
	}
	need.BitsPerWeight = bpw

	if spec.Layers > 0 && spec.KVHeads > 0 && spec.HeadDim > 0 {
		kvBytesPerToken := 2 * float64(spec.Layers) * float64(spec.KVHeads) * float64(spec.HeadDim) * 2
		need.KVGB = float64(context) * kvBytesPerToken / 1e9
	} else {
		need.KVUnknown = true
	}

	need.RequiredGB = (spec.FileSizeGB+need.KVGB)*RuntimeOverhead + RuntimeFixedGB
	need.MemoryScore = Interpolate(MemoryNeedCurve, need.RequiredGB)

	active := spec.ActiveParamsB
	if active <= 0 {
		active = spec.ParamsB
	}
	need.ActiveWeightGB = active * bpw / 8
	need.DemandScore = Interpolate(DemandCurve, need.ActiveWeightGB)

	need.Score = Round1(Clamp(ModelWeightMemory*need.MemoryScore+ModelWeightDemand*need.DemandScore, 0, 100))
	need.RequiredGB = Round1(need.RequiredGB)
	need.KVGB = Round1(need.KVGB)
	need.ActiveWeightGB = Round1(need.ActiveWeightGB)
	need.MemoryScore = Round1(need.MemoryScore)
	need.DemandScore = Round1(need.DemandScore)
	return need
}

// Throughput is an estimate, never a measurement.
type Throughput struct {
	TokensPerSec float64 `json:"tokens_per_sec"`
	Low          float64 `json:"low"`
	High         float64 `json:"high"`
	Formula      string  `json:"formula"`
	Bottleneck   string  `json:"bottleneck"`
	IsEstimate   bool    `json:"is_estimate"`
}

// EstimateThroughput uses the memory-bandwidth roofline:
//
//	tok/s ≈ effective bandwidth (GB/s) ÷ active weight bytes (GB) × efficiency
//
// GPU efficiency 0.45, Apple Metal 0.42, CPU 0.30 (adjusted for SIMD). This is
// an upper-bound-style estimate; real numbers depend on backend, batch size,
// context length, and whether layers spill out of VRAM.
func EstimateThroughput(p *hardware.Profile, spec ModelSpec) Throughput {
	out := Throughput{IsEstimate: true}
	if p == nil {
		out.Formula = "not estimated: no hardware profile"
		return out
	}
	need := ComputeModelNeed(spec, 8192)
	weight := need.ActiveWeightGB
	if weight <= 0 {
		out.Formula = "not estimated: model weight size is unknown"
		return out
	}
	table, _ := gpu.Load()
	bw := 0.0
	efficiency := 0.30
	device := "CPU"
	note := ""
	if ds := p.DiscreteGPUs(); len(ds) > 0 {
		best := 0.0
		for _, g := range ds {
			v := g.BandwidthGBps
			if v <= 0 && table != nil {
				e, _ := table.LookupOrFallback(g.Name, g.Vendor)
				v = e.BandwidthGBps
			}
			if v > best {
				best = v
			}
		}
		if best <= 0 {
			best = 150
		}
		bw = best
		efficiency = 0.45
		device = "GPU"
		if len(ds) > 1 {
			note = " using the fastest GPU only; multi-GPU scaling varies"
		}
	} else if u, ok := p.UnifiedGPU(); ok {
		bw = u.BandwidthGBps
		if bw <= 0 && table != nil {
			e, _ := table.LookupOrFallback(u.Name, "Apple")
			bw = e.BandwidthGBps
		}
		if bw <= 0 {
			bw = 100
		}
		efficiency = 0.42
		device = "Apple GPU"
	} else {
		bw = p.RAM.BandwidthGBps
		if bw <= 0 {
			bw = 30
		}
		factor, _ := simdFactor(p.CPU.SIMD)
		efficiency = 0.30 * (factor / 1.25)
		device = "CPU"
	}
	tps := Clamp(bw/weight*efficiency, 0.1, 250)
	out.TokensPerSec = Round1(tps)
	out.Low = Round1(tps * 0.65)
	out.High = Round1(tps * 1.35)
	out.Bottleneck = device
	out.Formula = fmt.Sprintf("%.0f GB/s ÷ %.2f GB active weights × %.2f efficiency = %.1f tok/s (%s, memory-bandwidth roofline%s)",
		bw, weight, efficiency, tps, device, note)
	if tps >= 249.9 {
		out.Formula += "; capped at 250 tok/s because small models become compute- or overhead-limited"
	}
	return out
}
