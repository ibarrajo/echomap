package challenge

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/elninja/echomap/internal/geo"
)

// Manager handles challenge token generation, validation, and probe selection.
type Manager struct {
	secret []byte
	ttl    time.Duration
	mu     sync.Mutex
	tokens map[string]tokenEntry
}

type tokenEntry struct {
	token     string
	clientID  string
	expiresAt time.Time
}

// ChallengeToken is returned from GenerateToken.
type ChallengeToken struct {
	ChallengeID string
	Token       string
	ExpiresAt   int64
}

// Probe represents a probe endpoint for latency measurement.
type Probe struct {
	ID         string
	Host       string
	Lat        float64
	Lon        float64
	Port       int
	Region     string  // continent/region tag for diversity selection
	DistanceKM float64 // distance from the estimated user location
}

// 27 Vultr speedtest servers — verified TCP port 80, global coverage.
var knownProbes = []Probe{
	// North America — US (6 regions)
	{ID: "lax-1", Host: "lax-ca-us-ping.vultr.com", Lat: 33.9425, Lon: -118.4081, Port: 80, Region: "na-west"},
	{ID: "sea-1", Host: "wa-us-ping.vultr.com", Lat: 47.6062, Lon: -122.3321, Port: 80, Region: "na-west"},
	{ID: "dfw-1", Host: "tx-us-ping.vultr.com", Lat: 32.7767, Lon: -96.7970, Port: 80, Region: "na-central"},
	{ID: "ord-1", Host: "il-us-ping.vultr.com", Lat: 41.8781, Lon: -87.6298, Port: 80, Region: "na-central"},
	{ID: "ewr-1", Host: "nj-us-ping.vultr.com", Lat: 40.7128, Lon: -74.0060, Port: 80, Region: "na-east"},
	{ID: "atl-1", Host: "ga-us-ping.vultr.com", Lat: 33.7490, Lon: -84.3880, Port: 80, Region: "na-east"},
	{ID: "mia-1", Host: "fl-us-ping.vultr.com", Lat: 25.7617, Lon: -80.1918, Port: 80, Region: "na-east"},

	// North America — Canada + Mexico
	{ID: "yto-1", Host: "tor-ca-ping.vultr.com", Lat: 43.6532, Lon: -79.3832, Port: 80, Region: "na-east"},
	{ID: "mex-1", Host: "mex-mx-ping.vultr.com", Lat: 19.4326, Lon: -99.1332, Port: 80, Region: "na-central"},

	// South America
	{ID: "gru-1", Host: "sao-br-ping.vultr.com", Lat: -23.5505, Lon: -46.6333, Port: 80, Region: "sa"},
	{ID: "scl-1", Host: "scl-cl-ping.vultr.com", Lat: -33.4489, Lon: -70.6693, Port: 80, Region: "sa"},

	// Europe — West
	{ID: "lon-1", Host: "lon-gb-ping.vultr.com", Lat: 51.5074, Lon: -0.1278, Port: 80, Region: "eu-west"},
	{ID: "par-1", Host: "par-fr-ping.vultr.com", Lat: 48.8566, Lon: 2.3522, Port: 80, Region: "eu-west"},
	{ID: "ams-1", Host: "ams-nl-ping.vultr.com", Lat: 52.3676, Lon: 4.9041, Port: 80, Region: "eu-west"},
	{ID: "fra-1", Host: "fra-de-ping.vultr.com", Lat: 50.1109, Lon: 8.6821, Port: 80, Region: "eu-west"},
	{ID: "mad-1", Host: "mad-es-ping.vultr.com", Lat: 40.4168, Lon: -3.7038, Port: 80, Region: "eu-west"},
	{ID: "man-1", Host: "man-uk-ping.vultr.com", Lat: 53.4808, Lon: -2.2426, Port: 80, Region: "eu-west"},

	// Europe — North/East
	{ID: "sto-1", Host: "sto-se-ping.vultr.com", Lat: 59.3293, Lon: 18.0686, Port: 80, Region: "eu-north"},
	{ID: "waw-1", Host: "waw-pl-ping.vultr.com", Lat: 52.2297, Lon: 21.0122, Port: 80, Region: "eu-east"},

	// Middle East
	{ID: "tlv-1", Host: "tlv-il-ping.vultr.com", Lat: 32.0853, Lon: 34.7818, Port: 80, Region: "me"},

	// Asia
	{ID: "hnd-1", Host: "hnd-jp-ping.vultr.com", Lat: 35.6762, Lon: 139.6503, Port: 80, Region: "asia-east"},
	{ID: "osk-1", Host: "osk-jp-ping.vultr.com", Lat: 34.6937, Lon: 135.5023, Port: 80, Region: "asia-east"},
	{ID: "sel-1", Host: "sel-kor-ping.vultr.com", Lat: 37.5665, Lon: 126.9780, Port: 80, Region: "asia-east"},
	{ID: "sin-1", Host: "sgp-ping.vultr.com", Lat: 1.3521, Lon: 103.8198, Port: 80, Region: "asia-se"},
	{ID: "bom-1", Host: "bom-in-ping.vultr.com", Lat: 19.0760, Lon: 72.8777, Port: 80, Region: "asia-south"},
	{ID: "del-1", Host: "del-in-ping.vultr.com", Lat: 28.7041, Lon: 77.1025, Port: 80, Region: "asia-south"},
	{ID: "blr-1", Host: "blr-in-ping.vultr.com", Lat: 12.9716, Lon: 77.5946, Port: 80, Region: "asia-south"},

	// Oceania
	{ID: "syd-1", Host: "syd-au-ping.vultr.com", Lat: -33.8688, Lon: 151.2093, Port: 80, Region: "oceania"},
	{ID: "mel-1", Host: "mel-au-ping.vultr.com", Lat: -37.8136, Lon: 144.9631, Port: 80, Region: "oceania"},

	// Africa
	{ID: "jnb-1", Host: "jnb-za-ping.vultr.com", Lat: -26.2041, Lon: 28.0473, Port: 80, Region: "africa"},
}

// NewManager creates a new challenge manager.
func NewManager(secret string, ttl time.Duration) *Manager {
	return &Manager{
		secret: []byte(secret),
		ttl:    ttl,
		tokens: make(map[string]tokenEntry),
	}
}

// GenerateToken creates a new challenge token for the given client.
func (m *Manager) GenerateToken(clientID string) (ChallengeToken, error) {
	// Generate random challenge ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return ChallengeToken{}, fmt.Errorf("generate random: %w", err)
	}
	challengeID := hex.EncodeToString(idBytes)

	// Generate HMAC token from challenge ID + client ID + timestamp
	expiresAt := time.Now().Add(m.ttl)
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(challengeID + clientID + fmt.Sprintf("%d", expiresAt.UnixNano())))
	token := hex.EncodeToString(mac.Sum(nil))

	// Store for validation
	m.mu.Lock()
	m.tokens[challengeID] = tokenEntry{
		token:     token,
		clientID:  clientID,
		expiresAt: expiresAt,
	}
	m.mu.Unlock()

	return ChallengeToken{
		ChallengeID: challengeID,
		Token:       token,
		ExpiresAt:   expiresAt.Unix(),
	}, nil
}

// GetClientID returns the client ID associated with a challenge (before consuming it).
func (m *Manager) GetClientID(challengeID string) string {
	m.mu.Lock()
	entry, exists := m.tokens[challengeID]
	m.mu.Unlock()
	if !exists {
		return ""
	}
	return entry.clientID
}

// ValidateToken checks a challenge token. Tokens are single-use — consumed on validation.
func (m *Manager) ValidateToken(challengeID, token string) error {
	m.mu.Lock()
	entry, exists := m.tokens[challengeID]
	if exists {
		delete(m.tokens, challengeID) // single-use: consume immediately
	}
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("unknown challenge ID")
	}

	if time.Now().After(entry.expiresAt) {
		return fmt.Errorf("token expired")
	}

	if !hmac.Equal([]byte(entry.token), []byte(token)) {
		return fmt.Errorf("invalid token")
	}

	return nil
}

// SelectProbes picks probes optimized for geolocation accuracy.
//
// Strategy: guarantee geographic diversity first, then fill with distance-based selection.
// 1. Pick 1 probe from each unique region (ensures continental coverage)
// 2. Sort remaining by distance from estimated location
// 3. Fill to count with nearest remaining (for precision around the user)
//
// This ensures we always have probes to both confirm AND exclude regions,
// regardless of where the user is estimated to be.
func (m *Manager) SelectProbes(estLat, estLon float64, count int) []Probe {
	if count >= len(knownProbes) {
		result := make([]Probe, len(knownProbes))
		for i, p := range knownProbes {
			p.DistanceKM = geo.HaversineKM(estLat, estLon, p.Lat, p.Lon)
			result[i] = p
		}
		return result
	}

	// Step 1: Group probes by region
	regionProbes := make(map[string][]Probe)
	for _, p := range knownProbes {
		regionProbes[p.Region] = append(regionProbes[p.Region], p)
	}

	// Step 2: Pick the nearest probe from each region (geographic diversity)
	selected := make(map[string]bool)
	var result []Probe

	regions := make([]string, 0, len(regionProbes))
	for r := range regionProbes {
		regions = append(regions, r)
	}
	sort.Strings(regions) // deterministic order

	for _, region := range regions {
		probes := regionProbes[region]
		// Find nearest probe in this region to estimated location
		bestIdx := 0
		bestDist := math.MaxFloat64
		for i, p := range probes {
			d := geo.HaversineKM(estLat, estLon, p.Lat, p.Lon)
			if d < bestDist {
				bestDist = d
				bestIdx = i
			}
		}
		p := probes[bestIdx]
		p.DistanceKM = bestDist
		result = append(result, p)
		selected[p.ID] = true

		if len(result) >= count {
			break
		}
	}

	// Step 3: Fill remaining slots with nearest unselected probes
	if len(result) < count {
		type probeWithDist struct {
			Probe
			dist float64
		}
		var remaining []probeWithDist
		for _, p := range knownProbes {
			if !selected[p.ID] {
				d := geo.HaversineKM(estLat, estLon, p.Lat, p.Lon)
				remaining = append(remaining, probeWithDist{Probe: p, dist: d})
			}
		}
		sort.Slice(remaining, func(i, j int) bool {
			return remaining[i].dist < remaining[j].dist
		})
		for _, pwd := range remaining {
			if len(result) >= count {
				break
			}
			p := pwd.Probe
			p.DistanceKM = pwd.dist
			result = append(result, p)
		}
	}

	return result
}
