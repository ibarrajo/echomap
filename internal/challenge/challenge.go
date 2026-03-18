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
	DistanceKM float64 // distance from the estimated user location
}

// Known global probe locations (a starter set covering major regions).
var knownProbes = []Probe{
	{ID: "fra-1", Host: "fra-de-ping.vultr.com", Lat: 50.1109, Lon: 8.6821, Port: 80},
	{ID: "lon-1", Host: "lon-gb-ping.vultr.com", Lat: 51.5074, Lon: -0.1278, Port: 80},
	{ID: "par-1", Host: "par-fr-ping.vultr.com", Lat: 48.8566, Lon: 2.3522, Port: 80},
	{ID: "ams-1", Host: "ams-nl-ping.vultr.com", Lat: 52.3676, Lon: 4.9041, Port: 80},
	{ID: "lax-1", Host: "lax-ca-us-ping.vultr.com", Lat: 33.9425, Lon: -118.4081, Port: 80},
	{ID: "hnd-1", Host: "hnd-jp-ping.vultr.com", Lat: 35.6762, Lon: 139.6503, Port: 80},
	{ID: "syd-1", Host: "syd-au-ping.vultr.com", Lat: -33.8688, Lon: 151.2093, Port: 80},
	{ID: "gru-1", Host: "sao-br-ping.vultr.com", Lat: -23.5505, Lon: -46.6333, Port: 80},
	{ID: "jnb-1", Host: "jnb-za-ping.vultr.com", Lat: -26.2041, Lon: 28.0473, Port: 80},
	{ID: "sin-1", Host: "sgp-ping.vultr.com", Lat: 1.3521, Lon: 103.8198, Port: 80},
	{ID: "bom-1", Host: "bom-in-ping.vultr.com", Lat: 19.0760, Lon: 72.8777, Port: 80},
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

// SelectProbes picks probes for a challenge based on the estimated user location.
// Returns a mix of near, far, and random probes.
func (m *Manager) SelectProbes(estLat, estLon float64, count int) []Probe {
	if count >= len(knownProbes) {
		count = len(knownProbes)
	}

	// Calculate distance from estimated location to each probe
	type probeWithDist struct {
		Probe
		dist float64
	}
	var withDist []probeWithDist
	for _, p := range knownProbes {
		d := geo.HaversineKM(estLat, estLon, p.Lat, p.Lon)
		withDist = append(withDist, probeWithDist{Probe: p, dist: d})
	}

	// Sort by distance
	sort.Slice(withDist, func(i, j int) bool {
		return withDist[i].dist < withDist[j].dist
	})

	// Selection strategy:
	// - ~50% nearest (for precision)
	// - ~33% farthest (for exclusion)
	// - ~17% middle (for anomaly detection)
	nearCount := int(math.Ceil(float64(count) * 0.5))
	farCount := int(math.Ceil(float64(count) * 0.33))
	midCount := count - nearCount - farCount
	if midCount < 0 {
		midCount = 0
		farCount = count - nearCount
	}

	selected := make(map[string]bool)
	var result []Probe

	// Add nearest
	for i := 0; i < len(withDist) && len(result) < nearCount; i++ {
		p := withDist[i].Probe
		p.DistanceKM = withDist[i].dist
		result = append(result, p)
		selected[p.ID] = true
	}

	// Add farthest
	for i := len(withDist) - 1; i >= 0 && farCount > 0; i-- {
		if !selected[withDist[i].Probe.ID] {
			p := withDist[i].Probe
			p.DistanceKM = withDist[i].dist
			result = append(result, p)
			selected[p.ID] = true
			farCount--
		}
	}

	// Add middle
	mid := len(withDist) / 2
	for i := mid; i < len(withDist) && midCount > 0; i++ {
		if !selected[withDist[i].Probe.ID] {
			p := withDist[i].Probe
			p.DistanceKM = withDist[i].dist
			result = append(result, p)
			selected[p.ID] = true
			midCount--
		}
	}

	return result
}
