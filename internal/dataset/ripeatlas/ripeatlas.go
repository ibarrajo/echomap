package ripeatlas

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/elninja/echomap/internal/dataset"
	"github.com/elninja/echomap/internal/geo"
)

const defaultBaseURL = "https://atlas.ripe.net"

// PingResult is a parsed RIPE Atlas ping measurement result.
type PingResult struct {
	ProbeID  int     `json:"probe_id"`
	DstAddr  string  `json:"dst_addr"`
	DstName  string  `json:"dst_name"`
	AvgRTT   float64 `json:"avg"`
	MinRTT   float64 `json:"min"`
	MaxRTT   float64 `json:"max"`
	Sent     int     `json:"sent"`
	Received int     `json:"rcvd"`
	Results  []struct {
		RTT float64 `json:"rtt"`
	} `json:"result"`
}

// ProbeInfo holds RIPE Atlas probe metadata.
type ProbeInfo struct {
	ID          int
	Lat         float64
	Lon         float64
	CountryCode string
}

// Client queries the RIPE Atlas API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Option configures the Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL (for testing).
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = url }
}

// NewClient creates a new RIPE Atlas API client.
func NewClient(opts ...Option) *Client {
	c := &Client{
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// FetchPingResults retrieves results for a ping measurement.
func (c *Client) FetchPingResults(ctx context.Context, measurementID int) ([]PingResult, error) {
	url := fmt.Sprintf("%s/api/v2/measurements/%d/results/", c.baseURL, measurementID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch results: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var results []PingResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode results: %w", err)
	}

	return results, nil
}

// FetchProbes retrieves probe metadata by ID.
func (c *Client) FetchProbes(ctx context.Context, probeIDs []int) (map[int]ProbeInfo, error) {
	ids := make([]string, len(probeIDs))
	for i, id := range probeIDs {
		ids[i] = fmt.Sprintf("%d", id)
	}

	url := fmt.Sprintf("%s/api/v2/probes/?id__in=%s&fields=id,geometry,country_code", c.baseURL, strings.Join(ids, ","))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch probes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var body struct {
		Results []struct {
			ID          int    `json:"id"`
			CountryCode string `json:"country_code"`
			Geometry    struct {
				Coordinates []float64 `json:"coordinates"` // [lon, lat] — GeoJSON order
			} `json:"geometry"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode probes: %w", err)
	}

	probes := make(map[int]ProbeInfo, len(body.Results))
	for _, p := range body.Results {
		lat, lon := 0.0, 0.0
		if len(p.Geometry.Coordinates) >= 2 {
			lon = p.Geometry.Coordinates[0]
			lat = p.Geometry.Coordinates[1]
		}
		probes[p.ID] = ProbeInfo{
			ID:          p.ID,
			Lat:         lat,
			Lon:         lon,
			CountryCode: p.CountryCode,
		}
	}

	return probes, nil
}

// BuildDataset fetches ping measurement results and probe metadata,
// then builds a dataset.Dataset suitable for the geolocation engine.
func (c *Client) BuildDataset(ctx context.Context, measurementIDs []int) (*dataset.Dataset, error) {
	// Collect all results and unique probe IDs
	var allResults []PingResult
	probeIDSet := make(map[int]bool)

	for _, msm := range measurementIDs {
		results, err := c.FetchPingResults(ctx, msm)
		if err != nil {
			return nil, fmt.Errorf("measurement %d: %w", msm, err)
		}
		for _, r := range results {
			allResults = append(allResults, r)
			probeIDSet[r.ProbeID] = true
		}
	}

	// Fetch probe locations
	probeIDs := make([]int, 0, len(probeIDSet))
	for id := range probeIDSet {
		probeIDs = append(probeIDs, id)
	}

	probes, err := c.FetchProbes(ctx, probeIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch probes: %w", err)
	}

	// Build CSV-compatible entries: source (probe location) → destination
	// Group by probe, create city names from country codes
	var entries []dataset.RawEntry
	for _, r := range allResults {
		probe, ok := probes[r.ProbeID]
		if !ok {
			continue
		}

		srcName := fmt.Sprintf("probe-%d-%s", probe.ID, probe.CountryCode)

		entries = append(entries, dataset.RawEntry{
			SrcName: srcName,
			DstName: r.DstAddr,
			SrcLat:  probe.Lat,
			SrcLon:  probe.Lon,
			DstLat:  0, // We don't know destination coords from ping results
			DstLon:  0,
			AvgMS:   r.AvgRTT,
			MinMS:   r.MinRTT,
			MaxMS:   r.MaxRTT,
		})
	}

	return dataset.FromEntries(entries), nil
}

// interface compliance
var _ geo.DatasetQuerier = (*dataset.Dataset)(nil)
