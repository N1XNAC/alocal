package scoring

// Weight and reference constants. Changing a curve or weight changes every
// score; the README documents these exact values.
const (
	WeightMemory    = 0.50
	WeightBandwidth = 0.25
	WeightCompute   = 0.20
	WeightDisk      = 0.05

	DiscreteVRAMUsable = 0.95 // framebuffer/driver reserve
	UnifiedRAMUsable   = 0.70 // macOS realistically exposes ~65-75% to the GPU

	ModelWeightMemory = 0.70
	ModelWeightDemand = 0.30

	RuntimeOverhead = 1.12 // KV/scratch/allocator growth
	RuntimeFixedGB  = 0.35 // backend/context overhead
)

// MemoryNeedCurve maps required model memory in GB to 0-100.
var MemoryNeedCurve = []Point{
	{0.3, 3}, {0.5, 5}, {1, 9}, {1.5, 12}, {2, 15}, {3, 20}, {4, 25},
	{5, 29}, {6, 33}, {8, 40}, {10, 46}, {12, 51}, {16, 58}, {20, 63},
	{24, 68}, {32, 75}, {40, 80}, {48, 84}, {56, 87}, {64, 89}, {80, 93},
	{96, 95}, {128, 98}, {192, 100},
}

// BandwidthCurve maps GB/s to 0-100. It is deliberately log-like rather than
// linear: the jump from 50 to 100 GB/s matters much more than 900 to 1000.
var BandwidthCurve = []Point{
	{0, 0}, {25, 12}, {50, 25}, {100, 40}, {200, 54}, {300, 64}, {400, 72},
	{500, 78}, {600, 82}, {800, 88}, {1000, 91}, {1600, 96}, {2500, 99}, {4000, 100},
}

// DemandCurve maps active weight bytes (GB) to 0-100 compute/bandwidth demand.
var DemandCurve = []Point{
	{0.05, 1}, {0.1, 2}, {0.25, 5}, {0.5, 8}, {1, 13}, {2, 22}, {3, 28},
	{4, 33}, {6, 41}, {8, 47}, {12, 55}, {16, 61}, {24, 70}, {32, 77},
	{48, 85}, {64, 90}, {100, 96}, {200, 100},
}

// BitsPerWeight is the effective storage cost of each supported quantization.
// Q4_K_M is ~4.85 bpw rather than 4.0 because some tensors stay at higher
// precision.
var BitsPerWeight = map[string]float64{
	"Q3_K_M": 3.90,
	"Q4_K_M": 4.85,
	"Q5_K_M": 5.70,
	"Q6_K":   6.60,
	"Q8_0":   8.50,
	"FP16":   16.00,
}

// Quantizations lists accepted catalog quant labels.
var Quantizations = []string{"Q3_K_M", "Q4_K_M", "Q5_K_M", "Q6_K", "Q8_0", "FP16"}

// TierFor maps a 0-100 score to a human label.
func TierFor(score float64) string {
	switch {
	case score >= 85:
		return "Workstation"
	case score >= 70:
		return "Enthusiast"
	case score >= 50:
		return "High"
	case score >= 30:
		return "Mid"
	default:
		return "Entry"
	}
}
