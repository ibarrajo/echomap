package geo_test

import (
	"testing"

	"github.com/elninja/echomap/internal/geo"
)

// --- Enhanced Jitter: Coefficient of Variation ---

func TestAnalyzeJitter_NormalVariation(t *testing.T) {
	// Typical real network: ~10-20% CV
	rtts := []int{12000, 14000, 11000, 13000, 12500}
	result := geo.AnalyzeJitter(rtts)

	if !result.Normal {
		t.Error("normal network jitter should be flagged as normal")
	}
	if result.CV <= 0 {
		t.Error("CV should be positive for variable RTTs")
	}
}

func TestAnalyzeJitter_ZeroVariation(t *testing.T) {
	rtts := []int{50000, 50000, 50000, 50000, 50000}
	result := geo.AnalyzeJitter(rtts)

	if result.Normal {
		t.Error("zero variation should be suspicious")
	}
	if result.CV != 0 {
		t.Error("CV should be 0 for identical values")
	}
}

func TestAnalyzeJitter_HighRTTLowCV_Suspicious(t *testing.T) {
	// 200ms RTT with only 0.1% variation — VPN with artificial delay
	rtts := []int{200000, 200100, 200050, 200080, 200020}
	result := geo.AnalyzeJitter(rtts)

	if result.Normal {
		t.Error("very low CV on high RTT should be suspicious")
	}
}

func TestAnalyzeJitter_LowRTTLowCV_OK(t *testing.T) {
	// 2ms RTT with low variation — normal for nearby server
	rtts := []int{2000, 2100, 2050, 2080, 2020}
	result := geo.AnalyzeJitter(rtts)

	if !result.Normal {
		t.Error("low CV on low RTT is normal (nearby server)")
	}
}

func TestAnalyzeJitter_HighCV_Suspicious(t *testing.T) {
	// Wildly variable — unstable connection or tampering
	rtts := []int{5000, 50000, 3000, 80000, 10000}
	result := geo.AnalyzeJitter(rtts)

	if result.Normal {
		t.Error("extremely high CV should be suspicious")
	}
}

func TestAnalyzeJitter_SingleSample(t *testing.T) {
	rtts := []int{10000}
	result := geo.AnalyzeJitter(rtts)

	// Can't analyze jitter with 1 sample — assume normal
	if !result.Normal {
		t.Error("single sample should default to normal")
	}
}

// --- VPN Heuristic ---

func TestDetectVPN_AllProbesHighAndFlat(t *testing.T) {
	// All probes have similar high RTTs with low jitter — classic VPN signature
	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{120000, 120100, 120050}},
		{ProbeID: "lhr-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{118000, 118200, 118100}},
		{ProbeID: "nrt-1", ProbeLat: 35.6762, ProbeLon: 139.6503, RTTs: []int{122000, 122100, 122050}},
	}

	vpn := geo.DetectVPN(measurements)
	if !vpn {
		t.Error("all probes with similar high RTTs + low jitter should flag VPN")
	}
}

func TestDetectVPN_NormalSpread(t *testing.T) {
	// Real user: nearby probes fast, far probes slow
	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{7500, 8200, 7800}},
		{ProbeID: "lhr-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{8200, 9000, 8500}},
		{ProbeID: "nrt-1", ProbeLat: 35.6762, ProbeLon: 139.6503, RTTs: []int{245000, 260000, 255000}},
	}

	vpn := geo.DetectVPN(measurements)
	if vpn {
		t.Error("normal RTT spread should not flag VPN")
	}
}

func TestDetectVPN_SingleProbe(t *testing.T) {
	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{120000, 120100, 120050}},
	}

	vpn := geo.DetectVPN(measurements)
	if vpn {
		t.Error("can't detect VPN from single probe")
	}
}

// --- Multi-Probe Correlation ---

func TestCorrelateProbes_ConsistentGeography(t *testing.T) {
	// User in Amsterdam: closer probes have lower RTTs
	// RTTs scaled for TCP overhead model (30ms baseline subtracted before distance calc)
	// AMS ~40ms (nearby, within overhead), FRA ~50ms (slightly farther), NRT ~350ms (far)
	measurements := []geo.Measurement{
		{ProbeID: "ams-1", ProbeLat: 52.3676, ProbeLon: 4.9041, RTTs: []int{40000, 43000, 41000}},      // very close
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{55000, 58000, 56000}},       // nearby
		{ProbeID: "nrt-1", ProbeLat: 35.6762, ProbeLon: 139.6503, RTTs: []int{350000, 370000, 360000}}, // far
	}

	result := geo.CorrelateProbes(measurements)
	if !result.Consistent {
		t.Error("geographically consistent RTTs should pass correlation")
	}
}

func TestCorrelateProbes_Inconsistent(t *testing.T) {
	// Two probes ~350km apart (Frankfurt and Amsterdam), but RTTs imply user
	// is only 50km from Amsterdam and 50km from a point 20,000km from Frankfurt.
	// max_dist_ams(1ms) + max_dist_fra(1ms) = 100 + 100 = 200km < 350km (probe distance)
	// Triangle inequality violated — no user position can produce both RTTs.
	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{500, 600, 550}},   // 0.5ms → 50km max
		{ProbeID: "ams-1", ProbeLat: 52.3676, ProbeLon: 4.9041, RTTs: []int{500, 600, 550}},    // 0.5ms → 50km max
	}
	// Probes are ~350km apart, but user claims to be within 50km of BOTH — impossible

	result := geo.CorrelateProbes(measurements)
	if result.Consistent {
		t.Error("impossible geographic pattern should fail correlation")
	}
}
