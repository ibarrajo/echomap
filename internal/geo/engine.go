package geo

// Engine wraps the geolocation calculation functions into an injectable service.
type Engine struct{}

// NewEngine creates a new geolocation engine.
func NewEngine() *Engine {
	return &Engine{}
}

// LocateResult holds the output of a geolocation calculation.
type LocateResult struct {
	Region     Region
	Confidence float64
	Exclusions []Exclusion
	Spoofing   SpoofingIndicators
	ProbeResults []ProbeResult
}

// SpoofingIndicators flags suspicious measurement patterns.
type SpoofingIndicators struct {
	VPNLikely           bool
	JitterAbnormal      bool
	RatioInconsistent   bool
	PhysicallyImpossible bool
}

// ProbeResult holds per-probe analysis.
type ProbeResult struct {
	ProbeID       string
	RTTMS         float64
	JitterMS      float64
	MaxDistanceKM float64
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

	// Confidence scoring
	confidence := e.score(region, len(measurements), anyJitterBad, ratioOK)

	return &LocateResult{
		Region:     region,
		Confidence: confidence,
		Exclusions: exclusions,
		Spoofing: SpoofingIndicators{
			JitterAbnormal:      anyJitterBad,
			RatioInconsistent:   !ratioOK,
			PhysicallyImpossible: region.RadiusKM == 0 && len(measurements) > 1,
		},
		ProbeResults: probeResults,
	}
}

func (e *Engine) score(region Region, probeCount int, jitterBad, ratioOK bool) float64 {
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

	if score > 1.0 {
		score = 1.0
	}
	if score < 0 {
		score = 0
	}
	return score
}
