package challenge_test

import (
	"testing"
	"time"

	"github.com/elninja/echomap/internal/challenge"
)

// --- Token Generation ---

func TestGenerateToken_NotEmpty(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	tok, err := mgr.GenerateToken("client-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.ChallengeID == "" {
		t.Error("challenge ID should not be empty")
	}
	if tok.Token == "" {
		t.Error("token should not be empty")
	}
}

func TestGenerateToken_UniquePerCall(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	tok1, _ := mgr.GenerateToken("client-123")
	tok2, _ := mgr.GenerateToken("client-123")
	if tok1.ChallengeID == tok2.ChallengeID {
		t.Error("each call should produce a unique challenge ID")
	}
	if tok1.Token == tok2.Token {
		t.Error("each call should produce a unique token")
	}
}

func TestGenerateToken_HasExpiry(t *testing.T) {
	ttl := time.Second * 10
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", ttl)
	tok, _ := mgr.GenerateToken("client-123")

	// Expiry should be ~10 seconds from now
	now := time.Now().Unix()
	if tok.ExpiresAt < now || tok.ExpiresAt > now+15 {
		t.Errorf("expiry should be ~10s from now, got delta=%d", tok.ExpiresAt-now)
	}
}

// --- Token Validation ---

func TestValidateToken_Valid(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	tok, _ := mgr.GenerateToken("client-123")

	err := mgr.ValidateToken(tok.ChallengeID, tok.Token)
	if err != nil {
		t.Errorf("valid token should pass validation: %v", err)
	}
}

func TestValidateToken_WrongToken(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	tok, _ := mgr.GenerateToken("client-123")

	err := mgr.ValidateToken(tok.ChallengeID, "wrong-token")
	if err == nil {
		t.Error("wrong token should fail validation")
	}
}

func TestValidateToken_UnknownChallengeID(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	_, _ = mgr.GenerateToken("client-123")

	err := mgr.ValidateToken("unknown-id", "any-token")
	if err == nil {
		t.Error("unknown challenge ID should fail validation")
	}
}

func TestValidateToken_Expired(t *testing.T) {
	// Use a very short TTL
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Millisecond*1)
	tok, _ := mgr.GenerateToken("client-123")

	// Wait for expiry
	time.Sleep(time.Millisecond * 5)

	err := mgr.ValidateToken(tok.ChallengeID, tok.Token)
	if err == nil {
		t.Error("expired token should fail validation")
	}
}

func TestValidateToken_SingleUse(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	tok, _ := mgr.GenerateToken("client-123")

	// First validation succeeds
	err := mgr.ValidateToken(tok.ChallengeID, tok.Token)
	if err != nil {
		t.Fatalf("first validation should succeed: %v", err)
	}

	// Second validation fails — token consumed
	err = mgr.ValidateToken(tok.ChallengeID, tok.Token)
	if err == nil {
		t.Error("second validation should fail — token is single-use")
	}
}

// --- Probe Selection ---

func TestSelectProbes_ReturnsRequestedCount(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	probes := mgr.SelectProbes(50.0, 4.0, 6) // Brussels area, 6 probes
	if len(probes) != 6 {
		t.Errorf("expected 6 probes, got %d", len(probes))
	}
}

func TestSelectProbes_IncludesNearAndFar(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	probes := mgr.SelectProbes(50.0, 4.0, 6) // Brussels

	hasNear := false
	hasFar := false
	for _, p := range probes {
		// "Near" = within 2000 km, "Far" = beyond 5000 km
		if p.DistanceKM < 2000 {
			hasNear = true
		}
		if p.DistanceKM > 5000 {
			hasFar = true
		}
	}
	if !hasNear {
		t.Error("probe selection should include at least one near probe (<2000km)")
	}
	if !hasFar {
		t.Error("probe selection should include at least one far probe (>5000km)")
	}
}

func TestSelectProbes_NoDuplicates(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	probes := mgr.SelectProbes(50.0, 4.0, 6)

	seen := make(map[string]bool)
	for _, p := range probes {
		if seen[p.ID] {
			t.Errorf("duplicate probe: %s", p.ID)
		}
		seen[p.ID] = true
	}
}

func TestSelectProbes_HasValidCoordinates(t *testing.T) {
	mgr := challenge.NewManager("test-secret-key-32bytes!!!!!!!!", time.Second*10)
	probes := mgr.SelectProbes(50.0, 4.0, 6)

	for _, p := range probes {
		if p.Lat < -90 || p.Lat > 90 {
			t.Errorf("probe %s has invalid lat: %f", p.ID, p.Lat)
		}
		if p.Lon < -180 || p.Lon > 180 {
			t.Errorf("probe %s has invalid lon: %f", p.ID, p.Lon)
		}
		if p.Host == "" {
			t.Errorf("probe %s has empty host", p.ID)
		}
	}
}
