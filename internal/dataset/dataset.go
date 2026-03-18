package dataset

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/elninja/echomap/internal/geo"
)

// City is a named location with coordinates.
type City struct {
	Name string
	Lat  float64
	Lon  float64
}

// RTTRange holds the latency statistics between two cities.
type RTTRange struct {
	AvgMS float64
	MinMS float64
	MaxMS float64
}

// MatchResult is the output of BestMatch (also satisfies geo.DatasetMatchResult).
type MatchResult = geo.DatasetMatchResult

// RawEntry is an input record for building a Dataset programmatically.
type RawEntry struct {
	SrcName string
	DstName string
	SrcLat  float64
	SrcLon  float64
	DstLat  float64
	DstLon  float64
	AvgMS   float64
	MinMS   float64
	MaxMS   float64
}

// FromEntries builds a Dataset from raw entries (used by API adapters).
func FromEntries(entries []RawEntry) *Dataset {
	ds := &Dataset{
		entries: make(map[string]RTTRange),
		cities:  make(map[string]City),
	}
	for _, e := range entries {
		key := pairKey(e.SrcName, e.DstName)
		ds.entries[key] = RTTRange{AvgMS: e.AvgMS, MinMS: e.MinMS, MaxMS: e.MaxMS}
		if e.SrcLat != 0 || e.SrcLon != 0 {
			ds.cities[e.SrcName] = City{Name: e.SrcName, Lat: e.SrcLat, Lon: e.SrcLon}
		}
		if e.DstLat != 0 || e.DstLon != 0 {
			ds.cities[e.DstName] = City{Name: e.DstName, Lat: e.DstLat, Lon: e.DstLon}
		}
	}
	return ds
}

// Dataset holds parsed latency data for city-to-city lookups.
type Dataset struct {
	entries map[string]RTTRange // key: "CityA|CityB" (sorted alphabetically)
	cities  map[string]City
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// LoadCSV parses a WonderNetwork-style CSV into a Dataset.
func LoadCSV(path string) (*Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}

	ds := &Dataset{
		entries: make(map[string]RTTRange),
		cities:  make(map[string]City),
	}

	// Skip header
	for i := 1; i < len(records); i++ {
		row := records[i]
		if len(row) < 9 {
			continue
		}

		src := strings.TrimSpace(row[0])
		dst := strings.TrimSpace(row[1])
		avgMS, _ := strconv.ParseFloat(strings.TrimSpace(row[2]), 64)
		minMS, _ := strconv.ParseFloat(strings.TrimSpace(row[3]), 64)
		maxMS, _ := strconv.ParseFloat(strings.TrimSpace(row[4]), 64)
		srcLat, _ := strconv.ParseFloat(strings.TrimSpace(row[5]), 64)
		srcLon, _ := strconv.ParseFloat(strings.TrimSpace(row[6]), 64)
		dstLat, _ := strconv.ParseFloat(strings.TrimSpace(row[7]), 64)
		dstLon, _ := strconv.ParseFloat(strings.TrimSpace(row[8]), 64)

		key := pairKey(src, dst)
		ds.entries[key] = RTTRange{AvgMS: avgMS, MinMS: minMS, MaxMS: maxMS}

		ds.cities[src] = City{Name: src, Lat: srcLat, Lon: srcLon}
		ds.cities[dst] = City{Name: dst, Lat: dstLat, Lon: dstLon}
	}

	return ds, nil
}

// EntryCount returns the number of city-pair entries.
func (ds *Dataset) EntryCount() int {
	return len(ds.entries)
}

// Cities returns all known cities.
func (ds *Dataset) Cities() []City {
	result := make([]City, 0, len(ds.cities))
	for _, c := range ds.cities {
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// Lookup returns the RTT range for a city pair (bidirectional).
func (ds *Dataset) Lookup(cityA, cityB string) (RTTRange, bool) {
	r, ok := ds.entries[pairKey(cityA, cityB)]
	return r, ok
}

// LookupByProbeCoords finds the expected RTT between a probe (by lat/lon) and a city name.
// It matches the probe coordinates to the nearest known city.
func (ds *Dataset) LookupByProbeCoords(probeLat, probeLon float64, cityName string) (float64, bool) {
	// Find the closest city to the probe coordinates
	bestDist := math.MaxFloat64
	bestCity := ""
	for _, c := range ds.cities {
		d := geo.HaversineKM(probeLat, probeLon, c.Lat, c.Lon)
		if d < bestDist {
			bestDist = d
			bestCity = c.Name
		}
	}
	if bestCity == "" || bestCity == cityName {
		return 0, false
	}
	rng, ok := ds.Lookup(bestCity, cityName)
	if !ok {
		return 0, false
	}
	return rng.AvgMS, true
}

// CitiesInRegion returns all cities within the given region.
func (ds *Dataset) CitiesInRegion(region geo.Region) []City {
	var result []City
	for _, c := range ds.cities {
		dist := geo.HaversineKM(region.Lat, region.Lon, c.Lat, c.Lon)
		if dist <= region.RadiusKM {
			result = append(result, c)
		}
	}
	return result
}

// BestMatch finds the city whose known latency profile best matches the observed RTTs.
// probeToCity maps probe IDs to dataset city names.
func (ds *Dataset) BestMatch(measurements []geo.Measurement, probeToCity map[string]string) MatchResult {
	type scored struct {
		city  City
		err   float64
		count int // how many probes had data
	}

	var candidates []scored

	for _, candidate := range ds.cities {
		totalErr := 0.0
		matched := 0

		for _, m := range measurements {
			probeCityName, ok := probeToCity[m.ProbeID]
			if !ok {
				continue
			}

			expectedRTT, ok := ds.Lookup(candidate.Name, probeCityName)
			if !ok {
				continue
			}

			// Median observed RTT in ms
			observedMS := float64(medianInt(m.RTTs)) / 1000.0

			// Normalized error: |observed - expected| / expected
			if expectedRTT.AvgMS > 0 {
				normErr := math.Abs(observedMS-expectedRTT.AvgMS) / expectedRTT.AvgMS
				totalErr += normErr
				matched++
			}
		}

		if matched > 0 {
			candidates = append(candidates, scored{
				city:  candidate,
				err:   totalErr / float64(matched),
				count: matched,
			})
		}
	}

	if len(candidates) == 0 {
		return MatchResult{Error: math.MaxFloat64}
	}

	// Sort by error ascending, prefer more matches on tie
	sort.Slice(candidates, func(i, j int) bool {
		if math.Abs(candidates[i].err-candidates[j].err) < 0.01 {
			return candidates[i].count > candidates[j].count
		}
		return candidates[i].err < candidates[j].err
	})

	best := candidates[0]
	return MatchResult{
		City:  best.city.Name,
		Lat:   best.city.Lat,
		Lon:   best.city.Lon,
		Error: best.err,
	}
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
