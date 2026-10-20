package main

import (
	"math"
	"math/rand"
)

// wilson is the 95% Wilson score interval for k successes in n trials. It
// stays honest on small samples: 1/1 is not "100%", it is "somewhere 21–100%".
func wilson(k, n int) (lo, hi float64) {
	if n <= 0 {
		return 0, 1
	}
	if k > n {
		k = n
	}
	const z = 1.96
	nf := float64(n)
	p := float64(k) / nf
	den := 1 + z*z/nf
	mid := p + z*z/(2*nf)
	spread := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf))
	lo = (mid - spread) / den
	hi = (mid + spread) / den
	return math.Max(0, lo), math.Min(1, hi)
}

// betaSample draws from Beta(a, b) via two gamma draws.
func betaSample(r *rand.Rand, a, b float64) float64 {
	x := gammaSample(r, a)
	y := gammaSample(r, b)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// gammaSample uses Marsaglia–Tsang; shape < 1 is boosted and corrected.
func gammaSample(r *rand.Rand, shape float64) float64 {
	if shape <= 0 {
		return 0
	}
	if shape < 1 {
		return gammaSample(r, shape+1) * math.Pow(r.Float64(), 1/shape)
	}
	d := shape - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := r.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := r.Float64()
		if u < 1-0.0331*x*x*x*x || math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}
