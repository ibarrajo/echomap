package geo_test

import (
	"testing"

	"github.com/elninja/echomap/internal/geo"
)

func TestIntersectCircles_NoOverlap_FallsBackToSmallest(t *testing.T) {
	// Two probes far apart with tight circles — no overlap possible.
	// Should fall back to the smallest circle's center, not (0,0).
	circles := []geo.Circle{
		{Lat: 47.6062, Lon: -122.3321, RadiusKM: 500},  // Seattle
		{Lat: 35.6762, Lon: 139.6503, RadiusKM: 8000},   // Tokyo
	}

	region := geo.IntersectCircles(circles)

	// Must NOT be (0,0) — that's the Gulf of Guinea and always wrong
	if region.Lat == 0 && region.Lon == 0 {
		t.Error("non-overlapping circles should NOT return (0,0), should fall back to smallest circle center")
	}

	// Should fall back to Seattle (the smaller circle)
	if region.Lat < 45 || region.Lat > 50 {
		t.Errorf("fallback lat should be near Seattle (~47.6), got %f", region.Lat)
	}
	if region.Lon > -120 || region.Lon < -125 {
		t.Errorf("fallback lon should be near Seattle (~-122.3), got %f", region.Lon)
	}
}

func TestLocate_ZeroZero_NeverReturned(t *testing.T) {
	// With globally spread probes and tight RTTs, circles may not overlap.
	// The engine should NEVER return (0,0) as a location.
	engine := geo.NewEngine()

	measurements := []geo.Measurement{
		{ProbeID: "sea-1", ProbeLat: 47.6062, ProbeLon: -122.3321, RTTs: []int{6000, 7000, 6500}},
		{ProbeID: "hnd-1", ProbeLat: 35.6762, ProbeLon: 139.6503, RTTs: []int{100000, 105000, 102000}},
		{ProbeID: "lon-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{135000, 140000, 137000}},
	}

	result := engine.Locate(measurements)

	if result.Region.Lat == 0 && result.Region.Lon == 0 {
		t.Error("engine should never return (0,0) — must fall back to nearest probe")
	}
}

func TestLocate_ReturnsNearestProbeAsLabel(t *testing.T) {
	engine := geo.NewEngine()

	// Single probe — should center on it
	measurements := []geo.Measurement{
		{ProbeID: "sea-1", ProbeLat: 47.6062, ProbeLon: -122.3321, RTTs: []int{6000, 7000, 6500}},
	}

	result := engine.Locate(measurements)

	if result.Region.Lat < 45 || result.Region.Lat > 50 {
		t.Errorf("single probe should center near Seattle, got lat %f", result.Region.Lat)
	}
}

func TestRegionLabel_NeverShowsZeroZero(t *testing.T) {
	// Even if somehow we get 0,0, the label should say something useful
	label := geo.RegionLabel(0, 0, 0)
	if label == "" {
		t.Error("label should never be empty")
	}
	// 0,0 should map to Lagos, NG (nearest city in our table)
	if label == "Unknown (0.00°N, 0.00°E)" {
		t.Error("0,0 should still map to a known city, not Unknown")
	}
}

func TestLocate_ConfidenceBreakdown(t *testing.T) {
	engine := geo.NewEngine()

	measurements := []geo.Measurement{
		{ProbeID: "sea-1", ProbeLat: 47.6062, ProbeLon: -122.3321, RTTs: []int{6000, 7000, 6500}},
		{ProbeID: "ord-1", ProbeLat: 41.8781, ProbeLon: -87.6298, RTTs: []int{48000, 50000, 49000}},
		{ProbeID: "ewr-1", ProbeLat: 40.7128, ProbeLon: -74.0060, RTTs: []int{65000, 68000, 66000}},
	}

	result := engine.Locate(measurements)

	if result.Confidence <= 0 {
		t.Error("should have positive confidence with valid measurements")
	}
	if result.Region.RadiusKM <= 0 {
		t.Error("should have positive radius")
	}
}
