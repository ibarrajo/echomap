package globalping_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/elninja/echomap/internal/dataset/globalping"
)

func serveTestData(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/measurements", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/v1/measurements/abc123")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"id":"abc123","probesCount":3}`))
	})

	mux.HandleFunc("GET /v1/measurements/abc123", func(w http.ResponseWriter, r *http.Request) {
		data, _ := os.ReadFile("testdata/measurement_response.json")
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCreateMeasurement_ReturnsID(t *testing.T) {
	srv := serveTestData(t)
	client := globalping.NewClient(globalping.WithBaseURL(srv.URL))

	id, err := client.CreateMeasurement(context.Background(), globalping.MeasurementRequest{
		Target: "198.51.100.1",
		Type:   "ping",
		Locations: []globalping.Location{
			{Country: "NL"},
			{Country: "US"},
			{Country: "JP"},
		},
	})
	if err != nil {
		t.Fatalf("CreateMeasurement: %v", err)
	}
	if id == "" {
		t.Error("measurement ID should not be empty")
	}
}

func TestGetResults_ParsesProbes(t *testing.T) {
	srv := serveTestData(t)
	client := globalping.NewClient(globalping.WithBaseURL(srv.URL))

	results, err := client.GetResults(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetResults: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Amsterdam probe
	ams := results[0]
	if ams.City != "Amsterdam" {
		t.Errorf("first probe city: got %s, want Amsterdam", ams.City)
	}
	if ams.Country != "NL" {
		t.Errorf("first probe country: got %s, want NL", ams.Country)
	}
	if ams.AvgRTT < 7 || ams.AvgRTT > 8.5 {
		t.Errorf("Amsterdam avg RTT should be ~7.8ms, got %f", ams.AvgRTT)
	}
	if ams.Lat < 52 || ams.Lat > 53 {
		t.Errorf("Amsterdam lat should be ~52.37, got %f", ams.Lat)
	}
}

func TestGetResults_IncludesTimings(t *testing.T) {
	srv := serveTestData(t)
	client := globalping.NewClient(globalping.WithBaseURL(srv.URL))

	results, err := client.GetResults(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetResults: %v", err)
	}

	for _, r := range results {
		if len(r.RTTs) == 0 {
			t.Errorf("probe %s should have RTT timings", r.City)
		}
		if r.MinRTT <= 0 {
			t.Errorf("probe %s min RTT should be positive", r.City)
		}
		if r.MaxRTT <= 0 {
			t.Errorf("probe %s max RTT should be positive", r.City)
		}
	}
}

func TestPingTarget_EndToEnd(t *testing.T) {
	srv := serveTestData(t)
	client := globalping.NewClient(globalping.WithBaseURL(srv.URL))

	results, err := client.PingTarget(context.Background(), "198.51.100.1", []globalping.Location{
		{Country: "NL"},
		{Country: "US"},
		{Country: "JP"},
	})
	if err != nil {
		t.Fatalf("PingTarget: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Should span multiple continents
	continents := make(map[string]bool)
	for _, r := range results {
		continents[r.Continent] = true
	}
	if len(continents) < 2 {
		t.Error("results should span multiple continents")
	}
}
