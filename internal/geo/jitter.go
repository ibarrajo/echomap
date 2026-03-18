package geo

import (
	"math"
	"sort"
)

// JitterAnalysis holds enhanced jitter analysis results.
type JitterAnalysis struct {
	Normal   bool
	CV       float64 // coefficient of variation (stddev / mean)
	MeanUS   float64
	StdDevUS float64
}

// AnalyzeJitter performs enhanced jitter analysis using coefficient of variation.
// Rules:
//   - CV == 0 with multiple samples → suspicious (artificial delay)
//   - High RTT (>50ms) + very low CV (<1%) → suspicious (VPN with injected delay)
//   - Low RTT (<10ms) + low CV → normal (nearby server, expected)
//   - Very high CV (>80%) → suspicious (unstable or tampered)
//   - Single sample → can't analyze, assume normal
func AnalyzeJitter(rttsUS []int) JitterAnalysis {
	if len(rttsUS) <= 1 {
		return JitterAnalysis{Normal: true}
	}

	mean := meanFloat(rttsUS)
	stddev := stddevFloat(rttsUS, mean)

	cv := 0.0
	if mean > 0 {
		cv = stddev / mean
	}

	// Zero CV with multiple samples → identical values → artificial
	if cv == 0 {
		return JitterAnalysis{Normal: false, CV: 0, MeanUS: mean, StdDevUS: stddev}
	}

	// Very high CV → wildly unstable
	if cv > 0.8 {
		return JitterAnalysis{Normal: false, CV: cv, MeanUS: mean, StdDevUS: stddev}
	}

	// High RTT + low CV → VPN with injected delay
	if mean > 50000 && cv < 0.01 {
		return JitterAnalysis{Normal: false, CV: cv, MeanUS: mean, StdDevUS: stddev}
	}

	return JitterAnalysis{Normal: true, CV: cv, MeanUS: mean, StdDevUS: stddev}
}

// CorrelationResult holds probe correlation analysis.
type CorrelationResult struct {
	Consistent bool
}

// CorrelateProbes checks if RTT ordering is geographically consistent.
// If two probes are far apart but one is much faster from a claimed location,
// that's suspicious.
func CorrelateProbes(measurements []Measurement) CorrelationResult {
	if len(measurements) < 2 {
		return CorrelationResult{Consistent: true}
	}

	// For each pair: check if the probe with the lower RTT is also geographically
	// closer to the implied user location (estimated from the intersection).
	// A simple heuristic: sort by RTT, then check that distances between
	// successive probes don't violate physics.

	type probeRTT struct {
		m      Measurement
		median int
	}

	sorted := make([]probeRTT, len(measurements))
	for i, m := range measurements {
		sorted[i] = probeRTT{m: m, median: medianInt(m.RTTs)}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].median < sorted[j].median
	})

	// The probe with the lowest RTT is closest. For each subsequent probe,
	// its RTT should be explainable by its distance from the "closest" probe.
	closest := sorted[0]
	for i := 1; i < len(sorted); i++ {
		other := sorted[i]

		// Distance between these two probes
		probeDistance := HaversineKM(closest.m.ProbeLat, closest.m.ProbeLon, other.m.ProbeLat, other.m.ProbeLon)

		// RTT difference → distance difference
		rttDiffUS := other.median - closest.median
		if rttDiffUS < 0 {
			rttDiffUS = 0
		}
		impliedDistanceDiff := MaxDistanceFromRTT(rttDiffUS)

		// The implied distance difference should not vastly exceed the probe distance.
		// If the user is near probe A and far from probe B, the RTT difference
		// should roughly correspond to the extra path. Allow 3x tolerance for routing.
		if impliedDistanceDiff > 0 && probeDistance > 0 {
			ratio := impliedDistanceDiff / probeDistance
			// If RTT difference implies much more distance than probes are apart,
			// that's suspicious (but only if both are significant)
			if ratio > 3.0 && rttDiffUS > 10000 {
				// This is actually valid — user could be near probe A and far from probe B
				// even if probes are close together. Skip this check.
			}
		}

		// The real inconsistency check: if the "far" probe has a significantly
		// LOWER RTT than the "near" probe based on the user's estimated position.
		// This is already covered by CheckRatios, but let's add a direct check:
		// Can the user physically reach both probes with these RTTs?
		maxDistClosest := MaxDistanceFromRTT(closest.median)
		maxDistOther := MaxDistanceFromRTT(other.median)

		// Triangle inequality on the user position
		if maxDistClosest+maxDistOther < probeDistance {
			return CorrelationResult{Consistent: false}
		}
	}

	return CorrelationResult{Consistent: true}
}

// DetectVPN checks if measurements show a VPN pattern:
// all probes have similar high RTTs with low jitter.
func DetectVPN(measurements []Measurement) bool {
	if len(measurements) < 2 {
		return false
	}

	// Collect median RTTs
	medians := make([]float64, len(measurements))
	for i, m := range measurements {
		medians[i] = float64(medianInt(m.RTTs))
	}

	mean := 0.0
	for _, v := range medians {
		mean += v
	}
	mean /= float64(len(medians))

	// All RTTs should be high (> 50ms = 50000μs)
	if mean < 50000 {
		return false
	}

	// Check CV of median RTTs across probes — VPN makes them suspiciously similar
	stddev := 0.0
	for _, v := range medians {
		d := v - mean
		stddev += d * d
	}
	stddev = math.Sqrt(stddev / float64(len(medians)))

	cv := stddev / mean

	// Low CV across probes + high mean → VPN signature
	// Real users have dramatically different RTTs to geographically diverse probes
	if cv < 0.05 {
		// Also check per-probe jitter
		allLowJitter := true
		for _, m := range measurements {
			j := AnalyzeJitter(m.RTTs)
			if j.CV > 0.03 {
				allLowJitter = false
				break
			}
		}
		if allLowJitter {
			return true
		}
	}

	return false
}

func meanFloat(vals []int) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += float64(v)
	}
	return sum / float64(len(vals))
}

func stddevFloat(vals []int, mean float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	sumSq := 0.0
	for _, v := range vals {
		d := float64(v) - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(vals)))
}
