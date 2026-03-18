package dataset_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/elninja/echomap/internal/dataset"
	"github.com/elninja/echomap/internal/geo"
)

func testdataPath(name string) string {
	return filepath.Join("testdata", name)
}

// --- CSV Parsing ---

func TestLoadCSV_ParsesAllRows(t *testing.T) {
	ds, err := dataset.LoadCSV(testdataPath("sample_pings.csv"))
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}

	// 20 rows in sample data, each produces a bidirectional entry
	if ds.EntryCount() < 20 {
		t.Errorf("expected at least 20 entries, got %d", ds.EntryCount())
	}
}

func TestLoadCSV_InvalidPath_ReturnsError(t *testing.T) {
	_, err := dataset.LoadCSV("nonexistent.csv")
	if err == nil {
		t.Error("should return error for missing file")
	}
}

func TestLoadCSV_ParsesCityCoordinates(t *testing.T) {
	ds, err := dataset.LoadCSV(testdataPath("sample_pings.csv"))
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}

	cities := ds.Cities()
	found := false
	for _, c := range cities {
		if c.Name == "Amsterdam" {
			found = true
			if math.Abs(c.Lat-52.3676) > 0.01 {
				t.Errorf("Amsterdam lat wrong: %f", c.Lat)
			}
			if math.Abs(c.Lon-4.9041) > 0.01 {
				t.Errorf("Amsterdam lon wrong: %f", c.Lon)
			}
			break
		}
	}
	if !found {
		t.Error("Amsterdam should be in city list")
	}
}

// --- Latency Lookup ---

func TestLookup_KnownPair(t *testing.T) {
	ds, err := dataset.LoadCSV(testdataPath("sample_pings.csv"))
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}

	rng, ok := ds.Lookup("Amsterdam", "Frankfurt")
	if !ok {
		t.Fatal("Amsterdam→Frankfurt should exist in dataset")
	}
	// CSV says avg=7.5, min=6.2, max=9.1
	if math.Abs(rng.AvgMS-7.5) > 0.1 {
		t.Errorf("avg should be ~7.5ms, got %f", rng.AvgMS)
	}
	if math.Abs(rng.MinMS-6.2) > 0.1 {
		t.Errorf("min should be ~6.2ms, got %f", rng.MinMS)
	}
	if math.Abs(rng.MaxMS-9.1) > 0.1 {
		t.Errorf("max should be ~9.1ms, got %f", rng.MaxMS)
	}
}

func TestLookup_Bidirectional(t *testing.T) {
	ds, err := dataset.LoadCSV(testdataPath("sample_pings.csv"))
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}

	// CSV has Amsterdam→Frankfurt, should also work as Frankfurt→Amsterdam
	rng, ok := ds.Lookup("Frankfurt", "Amsterdam")
	if !ok {
		t.Fatal("Frankfurt→Amsterdam should work (bidirectional)")
	}
	if rng.AvgMS <= 0 {
		t.Error("avg should be positive")
	}
}

func TestLookup_UnknownPair(t *testing.T) {
	ds, err := dataset.LoadCSV(testdataPath("sample_pings.csv"))
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}

	_, ok := ds.Lookup("Amsterdam", "MadeUpCity")
	if ok {
		t.Error("unknown pair should return ok=false")
	}
}

// --- Best Match ---

func TestBestMatch_FindsCorrectCity(t *testing.T) {
	ds, err := dataset.LoadCSV(testdataPath("sample_pings.csv"))
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}

	// Simulate a user in Amsterdam:
	// Amsterdam→Frankfurt ~7.5ms, Amsterdam→London ~8.2ms, Amsterdam→New York ~85ms
	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{7500, 8000, 7800}},
		{ProbeID: "lhr-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{8200, 8500, 8000}},
		{ProbeID: "nyc-1", ProbeLat: 40.7128, ProbeLon: -74.0060, RTTs: []int{85000, 87000, 86000}},
	}

	// Map probe IDs to city names for lookup
	probeToCity := map[string]string{
		"fra-1": "Frankfurt",
		"lhr-1": "London",
		"nyc-1": "New York",
	}

	match := ds.BestMatch(measurements, probeToCity)
	if match.City != "Amsterdam" {
		t.Errorf("expected Amsterdam, got %s", match.City)
	}
	if match.Error > 1.0 {
		t.Errorf("match error should be low for correct city, got %f", match.Error)
	}
}

func TestBestMatch_HighErrorForWrongCity(t *testing.T) {
	ds, err := dataset.LoadCSV(testdataPath("sample_pings.csv"))
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}

	// Simulate user with Tokyo-like RTTs but pretend to match against European cities
	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{240000, 245000, 242000}},
		{ProbeID: "lhr-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{255000, 260000, 258000}},
		{ProbeID: "nyc-1", ProbeLat: 40.7128, ProbeLon: -74.0060, RTTs: []int{180000, 185000, 182000}},
	}

	probeToCity := map[string]string{
		"fra-1": "Frankfurt",
		"lhr-1": "London",
		"nyc-1": "New York",
	}

	match := ds.BestMatch(measurements, probeToCity)
	// Should match Tokyo (closest latency profile), not a European city
	if match.City == "Amsterdam" || match.City == "Frankfurt" || match.City == "London" {
		t.Errorf("Tokyo-like RTTs should not match European city, got %s", match.City)
	}
}

// --- Cities In Region ---

func TestCitiesInRegion_FiltersCorrectly(t *testing.T) {
	ds, err := dataset.LoadCSV(testdataPath("sample_pings.csv"))
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}

	// Region centered on Western Europe, 1000km radius
	region := geo.Region{Lat: 50.0, Lon: 5.0, RadiusKM: 1000}
	cities := ds.CitiesInRegion(region)

	// Should include Amsterdam, Frankfurt, London, Paris
	names := make(map[string]bool)
	for _, c := range cities {
		names[c.Name] = true
	}

	for _, expected := range []string{"Amsterdam", "Frankfurt", "London", "Paris"} {
		if !names[expected] {
			t.Errorf("%s should be within 1000km of (50,5)", expected)
		}
	}

	// Should NOT include Tokyo, New York, Sydney
	for _, excluded := range []string{"Tokyo", "New York", "Sydney"} {
		if names[excluded] {
			t.Errorf("%s should NOT be within 1000km of (50,5)", excluded)
		}
	}
}

// --- Empty/Edge Cases ---

func TestLoadCSV_EmptyFile(t *testing.T) {
	// Create a temp file with just a header
	tmp := filepath.Join(t.TempDir(), "empty.csv")
	os.WriteFile(tmp, []byte("Source,Destination,Avg Latency (ms),Min Latency (ms),Max Latency (ms),Source Lat,Source Lon,Dest Lat,Dest Lon\n"), 0644)

	ds, err := dataset.LoadCSV(tmp)
	if err != nil {
		t.Fatalf("should handle empty CSV: %v", err)
	}
	if ds.EntryCount() != 0 {
		t.Errorf("empty CSV should have 0 entries, got %d", ds.EntryCount())
	}
}
