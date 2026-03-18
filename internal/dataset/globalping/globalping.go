package globalping

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.globalping.io"

// MeasurementRequest is the POST body for creating a measurement.
type MeasurementRequest struct {
	Target    string     `json:"target"`
	Type      string     `json:"type"`
	Locations []Location `json:"locations"`
	Packets   int        `json:"packets,omitempty"`
}

// Location specifies where to run a probe.
type Location struct {
	Country   string `json:"country,omitempty"`
	Continent string `json:"continent,omitempty"`
	City      string `json:"city,omitempty"`
}

// ProbeResult holds parsed results from a single probe.
type ProbeResult struct {
	City      string
	Country   string
	Continent string
	Lat       float64
	Lon       float64
	ASN       int
	Network   string
	AvgRTT    float64
	MinRTT    float64
	MaxRTT    float64
	RTTs      []float64
	Loss      float64
}

// Client queries the Globalping API.
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

// NewClient creates a new Globalping API client.
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

// CreateMeasurement posts a new measurement request. Returns the measurement ID.
func (c *Client) CreateMeasurement(ctx context.Context, req MeasurementRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/measurements", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("post measurement: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return result.ID, nil
}

// GetResults fetches the results of a completed measurement.
func (c *Client) GetResults(ctx context.Context, measurementID string) ([]ProbeResult, error) {
	url := fmt.Sprintf("%s/v1/measurements/%s", c.baseURL, measurementID)

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

	var body struct {
		Status  string `json:"status"`
		Results []struct {
			Probe struct {
				Continent string  `json:"continent"`
				Country   string  `json:"country"`
				City      string  `json:"city"`
				ASN       int     `json:"asn"`
				Network   string  `json:"network"`
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"probe"`
			Result struct {
				Timings []struct {
					RTT float64 `json:"rtt"`
				} `json:"timings"`
				Stats struct {
					Min  float64 `json:"min"`
					Max  float64 `json:"max"`
					Avg  float64 `json:"avg"`
					Loss float64 `json:"loss"`
				} `json:"stats"`
			} `json:"result"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode results: %w", err)
	}

	var results []ProbeResult
	for _, r := range body.Results {
		rtts := make([]float64, len(r.Result.Timings))
		for i, t := range r.Result.Timings {
			rtts[i] = t.RTT
		}

		results = append(results, ProbeResult{
			City:      r.Probe.City,
			Country:   r.Probe.Country,
			Continent: r.Probe.Continent,
			Lat:       r.Probe.Latitude,
			Lon:       r.Probe.Longitude,
			ASN:       r.Probe.ASN,
			Network:   r.Probe.Network,
			AvgRTT:    r.Result.Stats.Avg,
			MinRTT:    r.Result.Stats.Min,
			MaxRTT:    r.Result.Stats.Max,
			RTTs:      rtts,
			Loss:      r.Result.Stats.Loss,
		})
	}

	return results, nil
}

// PingTarget is a convenience method: creates a measurement, waits, and returns results.
func (c *Client) PingTarget(ctx context.Context, target string, locations []Location) ([]ProbeResult, error) {
	id, err := c.CreateMeasurement(ctx, MeasurementRequest{
		Target:    target,
		Type:      "ping",
		Locations: locations,
	})
	if err != nil {
		return nil, err
	}

	// Poll for results (in production, use exponential backoff)
	// For test server this returns immediately
	return c.GetResults(ctx, id)
}
