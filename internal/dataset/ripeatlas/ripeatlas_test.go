package ripeatlas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/elninja/echomap/internal/dataset/ripeatlas"
)

func serveTestData(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v2/measurements/1001/results/", func(w http.ResponseWriter, r *http.Request) {
		data, _ := os.ReadFile("testdata/ping_results.json")
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	mux.HandleFunc("/api/v2/probes/", func(w http.ResponseWriter, r *http.Request) {
		data, _ := os.ReadFile("testdata/probes.json")
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchPingResults_ParsesJSON(t *testing.T) {
	srv := serveTestData(t)
	client := ripeatlas.NewClient(ripeatlas.WithBaseURL(srv.URL))

	results, err := client.FetchPingResults(context.Background(), 1001)
	if err != nil {
		t.Fatalf("FetchPingResults: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First result: probe 6001 → dst fra-probe
	r := results[0]
	if r.ProbeID != 6001 {
		t.Errorf("probe_id: got %d, want 6001", r.ProbeID)
	}
	if r.AvgRTT < 7 || r.AvgRTT > 8.5 {
		t.Errorf("avg RTT should be ~7.8ms, got %f", r.AvgRTT)
	}
	if r.MinRTT < 7 || r.MinRTT > 8 {
		t.Errorf("min RTT should be ~7.5ms, got %f", r.MinRTT)
	}
}

func TestFetchProbes_ParsesCoordinates(t *testing.T) {
	srv := serveTestData(t)
	client := ripeatlas.NewClient(ripeatlas.WithBaseURL(srv.URL))

	probes, err := client.FetchProbes(context.Background(), []int{6001, 6002})
	if err != nil {
		t.Fatalf("FetchProbes: %v", err)
	}

	if len(probes) != 2 {
		t.Fatalf("expected 2 probes, got %d", len(probes))
	}

	// Probe 6001 should be in Amsterdam
	p := probes[6001]
	if p.CountryCode != "NL" {
		t.Errorf("probe 6001 country: got %s, want NL", p.CountryCode)
	}
	if p.Lat < 52 || p.Lat > 53 {
		t.Errorf("probe 6001 lat should be ~52.37, got %f", p.Lat)
	}
	if p.Lon < 4 || p.Lon > 5.5 {
		t.Errorf("probe 6001 lon should be ~4.9, got %f", p.Lon)
	}
}

func TestBuildDataset_CreatesLookupTable(t *testing.T) {
	srv := serveTestData(t)
	client := ripeatlas.NewClient(ripeatlas.WithBaseURL(srv.URL))

	ds, err := client.BuildDataset(context.Background(), []int{1001})
	if err != nil {
		t.Fatalf("BuildDataset: %v", err)
	}

	if ds.EntryCount() == 0 {
		t.Error("dataset should have entries")
	}

	cities := ds.Cities()
	if len(cities) == 0 {
		t.Error("dataset should have cities")
	}
}

func TestPingResult_UnmarshalJSON(t *testing.T) {
	raw := `{"probe_id":123,"avg":10.5,"min":9.2,"max":11.8,"dst_addr":"1.2.3.4","result":[{"rtt":10.5}]}`
	var r ripeatlas.PingResult
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.ProbeID != 123 {
		t.Errorf("probe_id: got %d, want 123", r.ProbeID)
	}
}
