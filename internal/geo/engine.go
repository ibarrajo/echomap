package geo

import (
	"math"
)

// DatasetQuerier is the interface the engine needs from a dataset.
// This avoids a circular import with the dataset package.
type DatasetQuerier interface {
	BestMatch(measurements []Measurement, probeToCity map[string]string) DatasetMatchResult
	LookupByProbeCoords(probeLat, probeLon float64, cityName string) (float64, bool)
}

// DatasetMatchResult is returned by BestMatch.
type DatasetMatchResult struct {
	City  string
	Lat   float64
	Lon   float64
	Error float64
}

// Engine wraps the geolocation calculation functions into an injectable service.
type Engine struct {
	dataset DatasetQuerier
}

// EngineOption configures the Engine.
type EngineOption func(*Engine)

// WithDataset adds a latency dataset for soft-bound calculations.
func WithDataset(ds DatasetQuerier) EngineOption {
	return func(e *Engine) {
		e.dataset = ds
	}
}

// NewEngine creates a new geolocation engine.
func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// LocateResult holds the output of a geolocation calculation.
type LocateResult struct {
	Region       Region
	Confidence   float64
	Exclusions   []Exclusion
	Spoofing     SpoofingIndicators
	ProbeResults []ProbeResult
	DatasetMatch *DatasetMatchInfo
}

// DatasetMatchInfo holds the dataset soft-bound result.
type DatasetMatchInfo struct {
	City  string
	Lat   float64
	Lon   float64
	Error float64
}

// SpoofingIndicators flags suspicious measurement patterns.
type SpoofingIndicators struct {
	VPNLikely            bool
	JitterAbnormal       bool
	RatioInconsistent    bool
	PhysicallyImpossible bool
}

// ProbeResult holds per-probe analysis.
type ProbeResult struct {
	ProbeID           string
	RTTMS             float64
	JitterMS          float64
	MaxDistanceKM     float64
	DatasetExpectedMS float64
}

// Locate computes a geolocation verdict from a set of measurements.
func (e *Engine) Locate(measurements []Measurement) *LocateResult {
	if len(measurements) == 0 {
		return &LocateResult{}
	}

	// Build circles from RTTs (Layer 1: speed of light)
	circles := make([]Circle, len(measurements))
	var probeResults []ProbeResult
	anyJitterBad := false

	for i, m := range measurements {
		medRTT := medianInt(m.RTTs)
		maxDist := MaxDistanceFromRTT(medRTT)
		circles[i] = Circle{Lat: m.ProbeLat, Lon: m.ProbeLon, RadiusKM: maxDist}

		// Jitter
		jResult := CheckJitter(m.RTTs)
		if !jResult.Normal {
			anyJitterBad = true
		}

		rttMS := float64(medRTT) / 1000.0
		jitterMS := float64(jResult.JitterUS) / 1000.0

		probeResults = append(probeResults, ProbeResult{
			ProbeID:       m.ProbeID,
			RTTMS:         rttMS,
			JitterMS:      jitterMS,
			MaxDistanceKM: maxDist,
		})
	}

	// Intersect circles
	region := IntersectCircles(circles)

	// Ratio analysis
	ratioOK := CheckRatios(measurements)

	// Exclusions
	exclusions := ComputeExclusions(circles)

	// Layer 2: Dataset soft bound
	var dsMatch *DatasetMatchInfo
	if e.dataset != nil {
		probeToCity := defaultProbeToCity()
		matchResult := e.dataset.BestMatch(measurements, probeToCity)
		if matchResult.City != "" {
			dsMatch = &DatasetMatchInfo{
				City:  matchResult.City,
				Lat:   matchResult.Lat,
				Lon:   matchResult.Lon,
				Error: matchResult.Error,
			}

			// Fill in dataset expected RTTs per probe
			for i, pr := range probeResults {
				expectedMS, ok := e.dataset.LookupByProbeCoords(
					measurements[i].ProbeLat, measurements[i].ProbeLon,
					matchResult.City,
				)
				if ok {
					probeResults[i] = ProbeResult{
						ProbeID:           pr.ProbeID,
						RTTMS:             pr.RTTMS,
						JitterMS:          pr.JitterMS,
						MaxDistanceKM:     pr.MaxDistanceKM,
						DatasetExpectedMS: expectedMS,
					}
				}
			}
		}
	}

	// Confidence scoring
	confidence := e.score(region, len(measurements), anyJitterBad, ratioOK, dsMatch)

	return &LocateResult{
		Region:       region,
		Confidence:   confidence,
		Exclusions:   exclusions,
		DatasetMatch: dsMatch,
		Spoofing: SpoofingIndicators{
			JitterAbnormal:       anyJitterBad,
			RatioInconsistent:    !ratioOK,
			PhysicallyImpossible: region.RadiusKM == 0 && len(measurements) > 1,
		},
		ProbeResults: probeResults,
	}
}

func (e *Engine) score(region Region, probeCount int, jitterBad, ratioOK bool, dsMatch *DatasetMatchInfo) float64 {
	if region.RadiusKM == 0 {
		return 0
	}

	score := 0.5

	// More probes → higher confidence
	if probeCount >= 6 {
		score += 0.2
	} else if probeCount >= 3 {
		score += 0.1
	}

	// Tighter region → higher confidence
	if region.RadiusKM < 500 {
		score += 0.2
	} else if region.RadiusKM < 2000 {
		score += 0.1
	}

	// Jitter and ratio checks
	if !jitterBad {
		score += 0.05
	} else {
		score -= 0.2
	}
	if ratioOK {
		score += 0.05
	} else {
		score -= 0.2
	}

	// Dataset match bonus
	if dsMatch != nil && dsMatch.Error < 0.3 {
		score += 0.15
	} else if dsMatch != nil && dsMatch.Error < 0.6 {
		score += 0.05
	}

	return math.Min(1.0, math.Max(0, score))
}

// defaultProbeToCity maps known probe IDs to dataset city names.
func defaultProbeToCity() map[string]string {
	return map[string]string{
		"fra-1": "Frankfurt",
		"lon-1": "London",
		"par-1": "Paris",
		"ams-1": "Amsterdam",
		"lax-1": "Los Angeles",
		"hnd-1": "Tokyo",
		"syd-1": "Sydney",
		"gru-1": "Sao Paulo",
		"jnb-1": "Johannesburg",
		"sin-1": "Singapore",
		"bom-1": "Mumbai",
	}
}
