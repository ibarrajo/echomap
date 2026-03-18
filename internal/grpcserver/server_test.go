package grpcserver_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/elninja/echomap/internal/challenge"
	"github.com/elninja/echomap/internal/config"
	"github.com/elninja/echomap/internal/geo"
	"github.com/elninja/echomap/internal/grpcserver"
	echomapv1 "github.com/elninja/echomap/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func setupTestServer(t *testing.T) echomapv1.EchoMapClient {
	t.Helper()

	cfg := config.Config{
		GRPCPort:   0,
		TokenTTL:   10 * time.Second,
		ProbeCount: 6,
		PingCount:  3,
		TimeoutMS:  5000,
		HMACSecret: "test-secret-key-32bytes!!!!!!!!!!",
	}
	mgr := challenge.NewManager(cfg.HMACSecret, cfg.TokenTTL)
	engine := geo.NewEngine()
	srv := grpcserver.NewHandler(cfg, mgr, engine)

	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	echomapv1.RegisterEchoMapServer(s, srv)

	go func() {
		if err := s.Serve(lis); err != nil {
			// Server stopped
		}
	}()
	t.Cleanup(func() { s.Stop() })

	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return echomapv1.NewEchoMapClient(conn)
}

// --- FetchChallenge ---

func TestFetchChallenge_ReturnsChallenge(t *testing.T) {
	client := setupTestServer(t)

	resp, err := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "test-client-1",
	})
	if err != nil {
		t.Fatalf("FetchChallenge failed: %v", err)
	}

	if resp.ChallengeId == "" {
		t.Error("challenge_id should not be empty")
	}
	if resp.Token == "" {
		t.Error("token should not be empty")
	}
	if len(resp.Targets) == 0 {
		t.Error("should return at least one probe target")
	}
	if resp.TimeoutMs <= 0 {
		t.Error("timeout should be positive")
	}
	if resp.ExpiresAt <= 0 {
		t.Error("expires_at should be set")
	}
}

func TestFetchChallenge_TargetsHaveValidFields(t *testing.T) {
	client := setupTestServer(t)

	resp, err := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "test-client-2",
	})
	if err != nil {
		t.Fatalf("FetchChallenge failed: %v", err)
	}

	for _, target := range resp.Targets {
		if target.Id == "" {
			t.Error("target ID should not be empty")
		}
		if target.Host == "" {
			t.Error("target host should not be empty")
		}
		if target.Port <= 0 {
			t.Errorf("target %s port should be positive, got %d", target.Id, target.Port)
		}
		if target.PingCount <= 0 {
			t.Errorf("target %s ping_count should be positive, got %d", target.Id, target.PingCount)
		}
	}
}

func TestFetchChallenge_UniqueChallengesPerCall(t *testing.T) {
	client := setupTestServer(t)

	resp1, _ := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{ClientId: "c1"})
	resp2, _ := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{ClientId: "c1"})

	if resp1.ChallengeId == resp2.ChallengeId {
		t.Error("each call should produce a unique challenge ID")
	}
}

// --- SubmitMeasurement ---

func TestSubmitMeasurement_ValidMeasurement(t *testing.T) {
	client := setupTestServer(t)

	// First, get a challenge
	challenge, err := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "test-client-3",
	})
	if err != nil {
		t.Fatalf("FetchChallenge failed: %v", err)
	}

	// Build measurements for all targets — simulate user near equator/prime meridian
	// Since probes are selected from (0,0), use large RTTs so circles overlap globally
	var measurements []*echomapv1.ProbeMeasurement
	for _, target := range challenge.Targets {
		// Use 250ms RTT — gives ~25,000 km radius circles, which overlap from anywhere
		measurements = append(measurements, &echomapv1.ProbeMeasurement{
			ProbeId: target.Id,
			RttsUs:  []int32{250000, 260000, 255000},
		})
	}

	resp, err := client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId:  challenge.ChallengeId,
		Token:        challenge.Token,
		Measurements: measurements,
	})
	if err != nil {
		t.Fatalf("SubmitMeasurement failed: %v", err)
	}

	if resp.Verdict == nil {
		t.Fatal("verdict should not be nil")
	}
	if resp.Verdict.Confidence <= 0 {
		t.Error("confidence should be positive for valid measurement")
	}
	if resp.Region == nil {
		t.Fatal("region should not be nil")
	}
	if resp.Region.RadiusKm <= 0 {
		t.Error("region radius should be positive")
	}
}

func TestSubmitMeasurement_InvalidToken(t *testing.T) {
	client := setupTestServer(t)

	challenge, _ := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "test-client-4",
	})

	_, err := client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId: challenge.ChallengeId,
		Token:       "wrong-token",
		Measurements: []*echomapv1.ProbeMeasurement{
			{ProbeId: "fra-1", RttsUs: []int32{8000}},
		},
	})
	if err == nil {
		t.Error("should reject invalid token")
	}
}

func TestSubmitMeasurement_ReplayRejected(t *testing.T) {
	client := setupTestServer(t)

	ch, _ := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "test-client-5",
	})

	measurements := []*echomapv1.ProbeMeasurement{
		{ProbeId: "fra-1", RttsUs: []int32{8000, 9000, 8500}},
	}

	// First submission succeeds
	_, err := client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId:  ch.ChallengeId,
		Token:        ch.Token,
		Measurements: measurements,
	})
	if err != nil {
		t.Fatalf("first submission should succeed: %v", err)
	}

	// Replay should fail — token is single-use
	_, err = client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId:  ch.ChallengeId,
		Token:        ch.Token,
		Measurements: measurements,
	})
	if err == nil {
		t.Error("replay should be rejected — token is single-use")
	}
}

func TestSubmitMeasurement_ReturnsProbeResults(t *testing.T) {
	client := setupTestServer(t)

	ch, _ := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "test-client-6",
	})

	var measurements []*echomapv1.ProbeMeasurement
	for _, target := range ch.Targets {
		measurements = append(measurements, &echomapv1.ProbeMeasurement{
			ProbeId: target.Id,
			RttsUs:  []int32{10000, 11000, 10500},
		})
	}

	resp, err := client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId:  ch.ChallengeId,
		Token:        ch.Token,
		Measurements: measurements,
	})
	if err != nil {
		t.Fatalf("SubmitMeasurement failed: %v", err)
	}

	if len(resp.ProbeResults) == 0 {
		t.Error("should return probe results")
	}

	for _, pr := range resp.ProbeResults {
		if pr.ProbeId == "" {
			t.Error("probe result should have probe_id")
		}
		if pr.RttMs <= 0 {
			t.Errorf("probe %s rtt_ms should be positive", pr.ProbeId)
		}
		if pr.MaxDistanceKm <= 0 {
			t.Errorf("probe %s max_distance_km should be positive", pr.ProbeId)
		}
	}
}

func TestSubmitMeasurement_ReturnsSpoofingIndicators(t *testing.T) {
	client := setupTestServer(t)

	ch, _ := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "test-client-7",
	})

	// Submit with zero jitter — should flag as suspicious
	var measurements []*echomapv1.ProbeMeasurement
	for _, target := range ch.Targets {
		measurements = append(measurements, &echomapv1.ProbeMeasurement{
			ProbeId: target.Id,
			RttsUs:  []int32{50000, 50000, 50000}, // zero jitter — suspicious
		})
	}

	resp, err := client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId:  ch.ChallengeId,
		Token:        ch.Token,
		Measurements: measurements,
	})
	if err != nil {
		t.Fatalf("SubmitMeasurement failed: %v", err)
	}

	if resp.Spoofing == nil {
		t.Fatal("spoofing indicators should not be nil")
	}
	if !resp.Spoofing.JitterAbnormal {
		t.Error("zero jitter should flag jitter_abnormal")
	}
}
