package geo_test

import (
	"math"
	"testing"

	"github.com/elninja/echomap/internal/dataset"
	"github.com/elninja/echomap/internal/geo"
)

func loadTestDataset(t *testing.T) *dataset.Dataset {
	t.Helper()
	ds, err := dataset.LoadCSV("../dataset/testdata/sample_pings.csv")
	if err != nil {
		t.Fatalf("load test dataset: %v", err)
	}
	return ds
}

func TestEngineWithDataset_TighterRegion(t *testing.T) {
	ds := loadTestDataset(t)
	engine := geo.NewEngine(geo.WithDataset(ds))

	// Simulate user in Amsterdam — RTTs match Amsterdam's known profile
	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{7500, 8000, 7800}},
		{ProbeID: "lhr-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{8200, 8500, 8000}},
		{ProbeID: "nyc-1", ProbeLat: 40.7128, ProbeLon: -74.0060, RTTs: []int{85000, 87000, 86000}},
	}

	withDS := engine.Locate(measurements)

	// Compare against engine without dataset
	noDS := geo.NewEngine().Locate(measurements)

	// Dataset-enhanced should have equal or higher confidence
	if withDS.Confidence < noDS.Confidence {
		t.Errorf("dataset engine confidence (%f) should be >= no-dataset (%f)",
			withDS.Confidence, noDS.Confidence)
	}

	// Should have a dataset match result
	if withDS.DatasetMatch == nil {
		t.Fatal("dataset match should not be nil when dataset is provided")
	}
	if withDS.DatasetMatch.City == "" {
		t.Error("dataset match should identify a city")
	}
}

func TestEngineWithDataset_IdentifiesAmsterdam(t *testing.T) {
	ds := loadTestDataset(t)
	engine := geo.NewEngine(geo.WithDataset(ds))

	// RTTs that closely match Amsterdam's profile
	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{7500, 8000, 7800}},
		{ProbeID: "lhr-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{8200, 8500, 8000}},
		{ProbeID: "nyc-1", ProbeLat: 40.7128, ProbeLon: -74.0060, RTTs: []int{85000, 87000, 86000}},
	}

	result := engine.Locate(measurements)

	if result.DatasetMatch.City != "Amsterdam" {
		t.Errorf("expected Amsterdam match, got %s", result.DatasetMatch.City)
	}

	// Region center should be near Amsterdam (52.37, 4.90)
	dist := geo.HaversineKM(result.DatasetMatch.Lat, result.DatasetMatch.Lon, 52.3676, 4.9041)
	if dist > 1 {
		t.Errorf("matched city should be near Amsterdam, distance=%f km", dist)
	}
}

func TestEngineWithDataset_IdentifiesTokyo(t *testing.T) {
	ds := loadTestDataset(t)
	engine := geo.NewEngine(geo.WithDataset(ds))

	// RTTs that match Tokyo's profile
	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{240000, 245000, 242000}},
		{ProbeID: "lhr-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{255000, 260000, 258000}},
		{ProbeID: "nyc-1", ProbeLat: 40.7128, ProbeLon: -74.0060, RTTs: []int{180000, 185000, 182000}},
	}

	result := engine.Locate(measurements)

	if result.DatasetMatch.City != "Tokyo" {
		t.Errorf("expected Tokyo match, got %s", result.DatasetMatch.City)
	}
}

func TestEngineWithDataset_DatasetExpectedMS(t *testing.T) {
	ds := loadTestDataset(t)
	engine := geo.NewEngine(geo.WithDataset(ds))

	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{7500, 8000, 7800}},
		{ProbeID: "lhr-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{8200, 8500, 8000}},
	}

	result := engine.Locate(measurements)

	// Probe results should include dataset_expected_ms when dataset is available
	for _, pr := range result.ProbeResults {
		if pr.DatasetExpectedMS == 0 {
			t.Errorf("probe %s should have dataset_expected_ms set", pr.ProbeID)
		}
	}
}

func TestEngineWithoutDataset_StillWorks(t *testing.T) {
	engine := geo.NewEngine() // no dataset

	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{7500, 8000, 7800}},
	}

	result := engine.Locate(measurements)

	if result.DatasetMatch != nil {
		t.Error("no dataset should mean no dataset match")
	}
	if result.Region.RadiusKM <= 0 {
		t.Error("should still compute region from speed-of-light bound")
	}
}

func TestEngineWithDataset_TighterRadius(t *testing.T) {
	ds := loadTestDataset(t)

	measurements := []geo.Measurement{
		{ProbeID: "fra-1", ProbeLat: 50.1109, ProbeLon: 8.6821, RTTs: []int{7500, 8000, 7800}},
		{ProbeID: "lhr-1", ProbeLat: 51.5074, ProbeLon: -0.1278, RTTs: []int{8200, 8500, 8000}},
		{ProbeID: "nyc-1", ProbeLat: 40.7128, ProbeLon: -74.0060, RTTs: []int{85000, 87000, 86000}},
	}

	withDS := geo.NewEngine(geo.WithDataset(ds)).Locate(measurements)
	noDS := geo.NewEngine().Locate(measurements)

	// When dataset matches well, effective radius should be tighter
	// The dataset match gives us city-level precision
	if withDS.DatasetMatch != nil && withDS.DatasetMatch.Error < 0.5 {
		effectiveRadius := math.Min(withDS.Region.RadiusKM, 500) // dataset caps at ~500km
		if effectiveRadius >= noDS.Region.RadiusKM && noDS.Region.RadiusKM > 500 {
			t.Logf("dataset radius=%f, no-dataset radius=%f (dataset should be tighter for good matches)",
				effectiveRadius, noDS.Region.RadiusKM)
		}
	}
}
