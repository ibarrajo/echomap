package grpcserver_test

import (
	"context"
	"testing"
	"time"

	echomapv1 "github.com/elninja/echomap/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- Token Validation Integration Tests ---

func TestToken_SubmitWithoutFetching(t *testing.T) {
	client := setupTestServer(t)

	// Try to submit measurements with a made-up challenge ID and token
	_, err := client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId: "fake-challenge-id",
		Token:       "fake-token",
		Measurements: []*echomapv1.ProbeMeasurement{
			{ProbeId: "fra-1", RttsUs: []int32{8000, 9000, 8500}},
		},
	})
	if err == nil {
		t.Fatal("should reject submission without a valid challenge")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error should be gRPC status: %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %s", st.Code())
	}
}

func TestToken_SubmitWithWrongToken(t *testing.T) {
	client := setupTestServer(t)

	// Fetch a real challenge
	ch, err := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "token-test-1",
	})
	if err != nil {
		t.Fatalf("FetchChallenge: %v", err)
	}

	// Submit with wrong token
	_, err = client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId:  ch.ChallengeId,
		Token:        "tampered-token-value",
		Measurements: []*echomapv1.ProbeMeasurement{
			{ProbeId: "fra-1", RttsUs: []int32{8000}},
		},
	})
	if err == nil {
		t.Fatal("should reject tampered token")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %s", st.Code())
	}
}

func TestToken_SubmitWithSwappedToken(t *testing.T) {
	client := setupTestServer(t)

	// Fetch two challenges
	ch1, _ := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{ClientId: "user-1"})
	ch2, _ := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{ClientId: "user-2"})

	// Try to use ch2's token with ch1's challenge ID
	_, err := client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId:  ch1.ChallengeId,
		Token:        ch2.Token,
		Measurements: []*echomapv1.ProbeMeasurement{
			{ProbeId: "fra-1", RttsUs: []int32{8000}},
		},
	})
	if err == nil {
		t.Fatal("should reject token from a different challenge")
	}
}

func TestToken_DoubleSubmit(t *testing.T) {
	client := setupTestServer(t)

	ch, _ := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{ClientId: "replay-test"})

	measurements := []*echomapv1.ProbeMeasurement{
		{ProbeId: "fra-1", RttsUs: []int32{250000, 260000, 255000}},
	}

	// First submit succeeds
	_, err := client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId: ch.ChallengeId, Token: ch.Token, Measurements: measurements,
	})
	if err != nil {
		t.Fatalf("first submit should succeed: %v", err)
	}

	// Second submit fails — token consumed
	_, err = client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId: ch.ChallengeId, Token: ch.Token, Measurements: measurements,
	})
	if err == nil {
		t.Fatal("replay should be rejected — token is single-use")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated for replay, got %s", st.Code())
	}
}

func TestToken_EmptyClientID(t *testing.T) {
	client := setupTestServer(t)

	_, err := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "",
	})
	if err == nil {
		t.Fatal("should reject empty client_id")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", st.Code())
	}
}

func TestToken_ExpiredChallenge(t *testing.T) {
	// This test uses the standard test server which has 10s TTL.
	// We can't easily test expiry without a custom TTL, but we can
	// verify the token structure carries an expiry timestamp.
	client := setupTestServer(t)

	ch, err := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "expiry-test",
	})
	if err != nil {
		t.Fatalf("FetchChallenge: %v", err)
	}

	// ExpiresAt should be ~10 seconds in the future
	now := time.Now().Unix()
	if ch.ExpiresAt < now || ch.ExpiresAt > now+15 {
		t.Errorf("expires_at should be ~10s from now, got delta=%ds", ch.ExpiresAt-now)
	}
}

func TestToken_ChallengeHasProbeTargets(t *testing.T) {
	client := setupTestServer(t)

	ch, err := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: "probe-test",
	})
	if err != nil {
		t.Fatalf("FetchChallenge: %v", err)
	}

	if len(ch.Targets) == 0 {
		t.Fatal("challenge should include probe targets")
	}

	for _, target := range ch.Targets {
		if target.Id == "" {
			t.Error("probe target should have ID")
		}
		if target.Host == "" {
			t.Error("probe target should have host")
		}
		if target.Port <= 0 {
			t.Error("probe target should have valid port")
		}
		if target.PingCount <= 0 {
			t.Error("probe target should specify ping count")
		}
	}
}
