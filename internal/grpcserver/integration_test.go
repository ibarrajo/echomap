package grpcserver_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/elninja/echomap/internal/challenge"
	"github.com/elninja/echomap/internal/config"
	"github.com/elninja/echomap/internal/geo"
	"github.com/elninja/echomap/internal/grpcserver"
	"github.com/elninja/echomap/internal/ratelimit"
	"github.com/elninja/echomap/internal/storage"
	echomapv1 "github.com/elninja/echomap/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// setupFullServer creates a server with storage, rate limiting, and enhanced jitter.
func setupFullServer(t *testing.T) (echomapv1.EchoMapClient, *storage.Repository) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "integration.db")
	store, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := config.Config{
		GRPCPort:        0,
		TokenTTL:        10 * time.Second,
		ProbeCount:      6,
		PingCount:       3,
		TimeoutMS:       5000,
		HMACSecret:      "test-secret-key-32bytes!!!!!!!!!!",
		RateLimitMax:    50,
		RateLimitWindow: time.Second,
	}
	mgr := challenge.NewManager(cfg.HMACSecret, cfg.TokenTTL)
	engine := geo.NewEngine()
	handler := grpcserver.NewHandler(cfg, mgr, engine).WithStorage(store)

	lim := ratelimit.New(cfg.RateLimitMax, cfg.RateLimitWindow)
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer(grpc.UnaryInterceptor(ratelimit.UnaryInterceptor(lim)))
	echomapv1.RegisterEchoMapServer(s, handler)

	go s.Serve(lis)
	t.Cleanup(func() { s.Stop() })

	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return echomapv1.NewEchoMapClient(conn), store
}

func TestIntegration_ResultPersistedToStorage(t *testing.T) {
	client, store := setupFullServer(t)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-client-id", "integ-client-1"))

	ch, err := client.FetchChallenge(ctx, &echomapv1.ChallengeRequest{ClientId: "integ-client-1"})
	if err != nil {
		t.Fatalf("FetchChallenge: %v", err)
	}

	var measurements []*echomapv1.ProbeMeasurement
	for _, target := range ch.Targets {
		measurements = append(measurements, &echomapv1.ProbeMeasurement{
			ProbeId: target.Id,
			RttsUs:  []int32{250000, 260000, 255000},
		})
	}

	_, err = client.SubmitMeasurement(ctx, &echomapv1.MeasurementRequest{
		ChallengeId:  ch.ChallengeId,
		Token:        ch.Token,
		Measurements: measurements,
	})
	if err != nil {
		t.Fatalf("SubmitMeasurement: %v", err)
	}

	// Verify it was persisted
	rec, err := store.GetResult(context.Background(), ch.ChallengeId)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if rec.ClientID != "integ-client-1" {
		t.Errorf("persisted client_id: got %s, want integ-client-1", rec.ClientID)
	}
	if rec.Confidence <= 0 {
		t.Error("persisted confidence should be positive")
	}
}

func TestIntegration_SuspiciousResultLogsAnomaly(t *testing.T) {
	client, store := setupFullServer(t)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-client-id", "integ-client-2"))

	ch, err := client.FetchChallenge(ctx, &echomapv1.ChallengeRequest{ClientId: "integ-client-2"})
	if err != nil {
		t.Fatalf("FetchChallenge: %v", err)
	}

	// Zero jitter — should trigger anomaly
	var measurements []*echomapv1.ProbeMeasurement
	for _, target := range ch.Targets {
		measurements = append(measurements, &echomapv1.ProbeMeasurement{
			ProbeId: target.Id,
			RttsUs:  []int32{100000, 100000, 100000},
		})
	}

	resp, err := client.SubmitMeasurement(ctx, &echomapv1.MeasurementRequest{
		ChallengeId:  ch.ChallengeId,
		Token:        ch.Token,
		Measurements: measurements,
	})
	if err != nil {
		t.Fatalf("SubmitMeasurement: %v", err)
	}

	// With TCP-calibrated circles, identical RTTs from global probes are physically impossible
	if resp.Verdict.Status != echomapv1.Status_STATUS_SUSPICIOUS && resp.Verdict.Status != echomapv1.Status_STATUS_REJECTED {
		t.Errorf("expected SUSPICIOUS or REJECTED, got %s", resp.Verdict.Status)
	}

	// Check anomalies were logged
	anomalies, err := store.GetAnomalies(context.Background(), "integ-client-2", 10)
	if err != nil {
		t.Fatalf("GetAnomalies: %v", err)
	}
	if len(anomalies) == 0 {
		t.Error("suspicious measurement should log at least one anomaly")
	}

	// Check result was marked suspicious
	rec, err := store.GetResult(context.Background(), ch.ChallengeId)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if !rec.Suspicious {
		t.Error("result should be marked suspicious")
	}
}

func setupRateLimitedServer(t *testing.T, maxReqs int) echomapv1.EchoMapClient {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ratelimit.db")
	store, _ := storage.New(dbPath)
	t.Cleanup(func() { store.Close() })

	cfg := config.Config{
		TokenTTL: 10 * time.Second, ProbeCount: 6, PingCount: 3, TimeoutMS: 5000,
		HMACSecret: "test-secret-key-32bytes!!!!!!!!!!",
		RateLimitMax: maxReqs, RateLimitWindow: time.Second,
	}
	mgr := challenge.NewManager(cfg.HMACSecret, cfg.TokenTTL)
	engine := geo.NewEngine()
	handler := grpcserver.NewHandler(cfg, mgr, engine).WithStorage(store)
	lim := ratelimit.New(cfg.RateLimitMax, cfg.RateLimitWindow)

	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer(grpc.UnaryInterceptor(ratelimit.UnaryInterceptor(lim)))
	echomapv1.RegisterEchoMapServer(s, handler)
	go s.Serve(lis)
	t.Cleanup(func() { s.Stop() })

	conn, _ := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	t.Cleanup(func() { conn.Close() })
	return echomapv1.NewEchoMapClient(conn)
}

func TestIntegration_RateLimiting(t *testing.T) {
	client := setupRateLimitedServer(t, 5)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-client-id", "rate-limit-client"))

	// Server is configured with max=5 per second
	for i := 0; i < 5; i++ {
		_, err := client.FetchChallenge(ctx, &echomapv1.ChallengeRequest{ClientId: "rate-limit-client"})
		if err != nil {
			t.Fatalf("request %d should succeed: %v", i+1, err)
		}
	}

	// 6th request should be rate limited
	_, err := client.FetchChallenge(ctx, &echomapv1.ChallengeRequest{ClientId: "rate-limit-client"})
	if err == nil {
		t.Fatal("6th request should be rate limited")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error should be gRPC status: %v", err)
	}
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted, got %s", st.Code())
	}
}

func TestIntegration_ClientHistory(t *testing.T) {
	client, store := setupFullServer(t)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-client-id", "history-client"))

	// Submit 3 measurements
	for i := 0; i < 3; i++ {
		ch, _ := client.FetchChallenge(ctx, &echomapv1.ChallengeRequest{ClientId: "history-client"})
		var measurements []*echomapv1.ProbeMeasurement
		for _, target := range ch.Targets {
			measurements = append(measurements, &echomapv1.ProbeMeasurement{
				ProbeId: target.Id,
				RttsUs:  []int32{250000, 260000, 255000},
			})
		}
		client.SubmitMeasurement(ctx, &echomapv1.MeasurementRequest{
			ChallengeId: ch.ChallengeId, Token: ch.Token, Measurements: measurements,
		})
	}

	history, err := store.GetClientHistory(context.Background(), "history-client", 10)
	if err != nil {
		t.Fatalf("GetClientHistory: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("expected 3 history records, got %d", len(history))
	}
}

func TestIntegration_VPNDetection(t *testing.T) {
	client, store := setupFullServer(t)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-client-id", "vpn-client"))

	ch, _ := client.FetchChallenge(ctx, &echomapv1.ChallengeRequest{ClientId: "vpn-client"})

	// VPN signature: all probes have similar high RTTs with zero jitter
	var measurements []*echomapv1.ProbeMeasurement
	for _, target := range ch.Targets {
		measurements = append(measurements, &echomapv1.ProbeMeasurement{
			ProbeId: target.Id,
			RttsUs:  []int32{120000, 120000, 120000}, // identical high RTTs
		})
	}

	resp, err := client.SubmitMeasurement(ctx, &echomapv1.MeasurementRequest{
		ChallengeId: ch.ChallengeId, Token: ch.Token, Measurements: measurements,
	})
	if err != nil {
		t.Fatalf("SubmitMeasurement: %v", err)
	}

	if !resp.Spoofing.VpnLikely {
		t.Error("should detect VPN pattern")
	}
	// VPN with identical RTTs from global probes may be SUSPICIOUS or REJECTED (physically impossible)
	if resp.Verdict.Status != echomapv1.Status_STATUS_SUSPICIOUS && resp.Verdict.Status != echomapv1.Status_STATUS_REJECTED {
		t.Errorf("VPN should be SUSPICIOUS or REJECTED, got %s", resp.Verdict.Status)
	}

	// Check anomaly was logged
	anomalies, err := store.GetAnomalies(context.Background(), "vpn-client", 10)
	if err != nil {
		t.Fatalf("GetAnomalies: %v", err)
	}

	hasVPN := false
	for _, a := range anomalies {
		if a.Type == "VPN_DETECTED" {
			hasVPN = true
		}
	}
	if !hasVPN {
		t.Error("VPN anomaly should be logged")
	}
}
