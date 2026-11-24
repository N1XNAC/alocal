package scoring

import "math"

// Point is one anchor of a piecewise-linear curve.
type Point struct {
	X float64
	Y float64
}

// Interpolate returns the linearly interpolated y for x, clamped to the first
// and last anchors. It is intentionally simple so scores are reproducible.
func Interpolate(points []Point, x float64) float64 {
	if len(points) == 0 {
		return 0
	}
	if x <= points[0].X {
		return points[0].Y
	}
	for i := 1; i < len(points); i++ {
		if x <= points[i].X {
			a, b := points[i-1], points[i]
			if b.X == a.X {
				return b.Y
			}
			t := (x - a.X) / (b.X - a.X)
			return a.Y + t*(b.Y-a.Y)
		}
	}
	return points[len(points)-1].Y
}

// Clamp bounds v to [lo, hi].
func Clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Round1 rounds to one decimal place for display and stable comparisons.
func Round1(v float64) float64 { return math.Round(v*10) / 10 }
