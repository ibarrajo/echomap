package geo

import (
	"math"
	"sort"
)

const (
	earthRadiusKM = 6371.0
	// Speed of light in fiber optic: ~200,000 km/s = 200 km/ms = 0.2 km/μs
	fiberSpeedKMPerUS = 0.2 // km per microsecond
)

// Circle represents a maximum-distance circle from a probe.
type Circle struct {
	Lat      float64
	Lon      float64
	RadiusKM float64
}

// Region represents the computed intersection of circles.
type Region struct {
	Lat      float64
	Lon      float64
	RadiusKM float64
}

// Exclusion represents a region that the user is definitely NOT in.
type Exclusion struct {
	Region     string
	Confidence float64
}

// JitterResult contains the result of jitter analysis.
type JitterResult struct {
	Normal   bool
	JitterUS int // jitter in microseconds
}

// Measurement is a single probe's data.
type Measurement struct {
	ProbeID  string
	ProbeLat float64
	ProbeLon float64
	RTTs     []int // microseconds
}

// HaversineKM returns the great-circle distance in km between two lat/lon points.
func HaversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	lat1R := toRad(lat1)
	lat2R := toRad(lat2)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1R)*math.Cos(lat2R)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKM * c
}

// MaxDistanceFromRTT converts a round-trip time (microseconds) to maximum distance (km).
// Uses speed of light in fiber: ~200 km/ms.
func MaxDistanceFromRTT(rttUS int) float64 {
	if rttUS <= 0 {
		return 0
	}
	// one-way = rtt / 2, distance = one-way * speed
	return float64(rttUS) / 2.0 * fiberSpeedKMPerUS
}

// IntersectCircles computes the approximate region where all circles overlap.
// Uses a grid-sampling approach over the bounding box of all circles.
func IntersectCircles(circles []Circle) Region {
	if len(circles) == 0 {
		return Region{}
	}
	if len(circles) == 1 {
		return Region{Lat: circles[0].Lat, Lon: circles[0].Lon, RadiusKM: circles[0].RadiusKM}
	}

	// Find bounding box of all circles
	minLat, maxLat := 90.0, -90.0
	minLon, maxLon := 180.0, -180.0
	for _, c := range circles {
		latDelta := c.RadiusKM / 111.0 // ~111 km per degree latitude
		lonDelta := c.RadiusKM / (111.0 * math.Cos(toRad(c.Lat)))
		if c.Lat-latDelta < minLat {
			minLat = c.Lat - latDelta
		}
		if c.Lat+latDelta > maxLat {
			maxLat = c.Lat + latDelta
		}
		if c.Lon-lonDelta < minLon {
			minLon = c.Lon - lonDelta
		}
		if c.Lon+lonDelta > maxLon {
			maxLon = c.Lon + lonDelta
		}
	}

	// Sample grid points and check which are inside ALL circles
	const gridSize = 100
	latStep := (maxLat - minLat) / gridSize
	lonStep := (maxLon - minLon) / gridSize

	var insideLats, insideLons []float64

	for i := 0; i <= gridSize; i++ {
		for j := 0; j <= gridSize; j++ {
			lat := minLat + float64(i)*latStep
			lon := minLon + float64(j)*lonStep

			allInside := true
			for _, c := range circles {
				dist := HaversineKM(lat, lon, c.Lat, c.Lon)
				if dist > c.RadiusKM {
					allInside = false
					break
				}
			}
			if allInside {
				insideLats = append(insideLats, lat)
				insideLons = append(insideLons, lon)
			}
		}
	}

	// No overlap — physically impossible
	if len(insideLats) == 0 {
		return Region{RadiusKM: 0}
	}

	// Compute centroid of inside points
	sumLat, sumLon := 0.0, 0.0
	for i := range insideLats {
		sumLat += insideLats[i]
		sumLon += insideLons[i]
	}
	centerLat := sumLat / float64(len(insideLats))
	centerLon := sumLon / float64(len(insideLons))

	// Radius = max distance from centroid to any inside point
	maxR := 0.0
	for i := range insideLats {
		d := HaversineKM(centerLat, centerLon, insideLats[i], insideLons[i])
		if d > maxR {
			maxR = d
		}
	}

	return Region{Lat: centerLat, Lon: centerLon, RadiusKM: maxR}
}

// ReferenceRegion is a named geographic region used for exclusion calculation.
type ReferenceRegion struct {
	Name string
	Lat  float64
	Lon  float64
}

var referenceRegions = []ReferenceRegion{
	{"East Asia", 35.0, 120.0},
	{"South Asia", 20.0, 78.0},
	{"Southeast Asia", 5.0, 105.0},
	{"Middle East", 30.0, 45.0},
	{"North America East", 40.0, -75.0},
	{"North America West", 37.0, -122.0},
	{"South America", -15.0, -55.0},
	{"Africa", 0.0, 25.0},
	{"Oceania", -25.0, 135.0},
	{"Western Europe", 48.0, 5.0},
	{"Eastern Europe", 52.0, 25.0},
	{"Northern Europe", 60.0, 15.0},
}

// ComputeExclusions determines which broad regions the user cannot be in,
// based on the circle constraints.
func ComputeExclusions(circles []Circle) []Exclusion {
	var exclusions []Exclusion

	for _, ref := range referenceRegions {
		// Check if this reference point is outside ALL circles
		// (i.e., every probe says user can't be this far)
		outsideCount := 0
		for _, c := range circles {
			dist := HaversineKM(ref.Lat, ref.Lon, c.Lat, c.Lon)
			if dist > c.RadiusKM {
				outsideCount++
			}
		}

		if outsideCount > 0 {
			// Confidence based on how many circles exclude this region
			confidence := float64(outsideCount) / float64(len(circles))
			if confidence >= 0.5 {
				exclusions = append(exclusions, Exclusion{Region: ref.Name, Confidence: confidence})
			}
		}
	}

	// Sort by confidence descending
	sort.Slice(exclusions, func(i, j int) bool {
		return exclusions[i].Confidence > exclusions[j].Confidence
	})

	return exclusions
}

// CheckJitter analyzes RTT variance to detect artificial delays.
// Real networks have natural jitter; spoofed delays are suspiciously consistent.
func CheckJitter(rttsUS []int) JitterResult {
	if len(rttsUS) < 2 {
		return JitterResult{Normal: true, JitterUS: 0}
	}

	// Calculate min and max
	minRTT, maxRTT := rttsUS[0], rttsUS[0]
	sum := 0
	for _, r := range rttsUS {
		if r < minRTT {
			minRTT = r
		}
		if r > maxRTT {
			maxRTT = r
		}
		sum += r
	}

	jitter := maxRTT - minRTT
	avgRTT := float64(sum) / float64(len(rttsUS))

	// Zero jitter is always suspicious
	if jitter == 0 {
		return JitterResult{Normal: false, JitterUS: 0}
	}

	// Jitter should be proportional to RTT. For high RTTs (>50ms),
	// jitter below 0.5% of avg RTT is suspicious.
	jitterRatio := float64(jitter) / avgRTT
	if avgRTT > 50000 && jitterRatio < 0.005 { // >50ms RTT, <0.5% jitter
		return JitterResult{Normal: false, JitterUS: jitter}
	}

	return JitterResult{Normal: true, JitterUS: jitter}
}

// CheckRatios verifies that the ratios between probe RTTs are consistent
// with a real physical location.
func CheckRatios(measurements []Measurement) bool {
	if len(measurements) < 2 {
		return true
	}

	// For each pair of probes, verify the RTT ratio is physically plausible.
	// The ratio of RTTs should roughly correspond to the ratio of distances
	// from the user's location to each probe.
	//
	// We don't know the user's location, but we can check for impossible
	// combinations: e.g., if probe A and B are 10,000 km apart, and RTTs
	// to both are <1ms, that's impossible.
	for i := 0; i < len(measurements); i++ {
		for j := i + 1; j < len(measurements); j++ {
			mi := measurements[i]
			mj := measurements[j]

			rttI := medianInt(mi.RTTs)
			rttJ := medianInt(mj.RTTs)

			distIJ := HaversineKM(mi.ProbeLat, mi.ProbeLon, mj.ProbeLat, mj.ProbeLon)
			maxDistI := MaxDistanceFromRTT(rttI)
			maxDistJ := MaxDistanceFromRTT(rttJ)

			// Triangle inequality: the user must be reachable from both probes.
			// If max distances from both probes don't overlap considering probe distance,
			// the measurement is impossible.
			if maxDistI+maxDistJ < distIJ {
				return false
			}
		}
	}

	return true
}

func toRad(deg float64) float64 {
	return deg * math.Pi / 180.0
}

func medianInt(vals []int) int {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]int, len(vals))
	copy(sorted, vals)
	sort.Ints(sorted)
	return sorted[len(sorted)/2]
}
