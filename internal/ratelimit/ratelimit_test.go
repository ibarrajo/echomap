package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/elninja/echomap/internal/ratelimit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestLimiter_AllowsWithinLimit(t *testing.T) {
	lim := ratelimit.New(5, time.Second) // 5 requests per second

	for i := 0; i < 5; i++ {
		if !lim.Allow("client-1") {
			t.Errorf("request %d should be allowed within limit", i+1)
		}
	}
}

func TestLimiter_BlocksOverLimit(t *testing.T) {
	lim := ratelimit.New(3, time.Second) // 3 per second

	for i := 0; i < 3; i++ {
		lim.Allow("client-1")
	}

	if lim.Allow("client-1") {
		t.Error("4th request should be blocked")
	}
}

func TestLimiter_SeparateClients(t *testing.T) {
	lim := ratelimit.New(2, time.Second)

	lim.Allow("client-1")
	lim.Allow("client-1")

	// client-2 should still have its own budget
	if !lim.Allow("client-2") {
		t.Error("client-2 should be allowed (separate budget)")
	}
}

func TestLimiter_ResetsAfterWindow(t *testing.T) {
	lim := ratelimit.New(2, 10*time.Millisecond) // very short window

	lim.Allow("client-1")
	lim.Allow("client-1")

	// Should be at limit
	if lim.Allow("client-1") {
		t.Error("should be at limit")
	}

	// Wait for window to pass
	time.Sleep(15 * time.Millisecond)

	if !lim.Allow("client-1") {
		t.Error("should be allowed after window reset")
	}
}

// --- gRPC Interceptor ---

func TestInterceptor_PassesWithinLimit(t *testing.T) {
	lim := ratelimit.New(10, time.Second)
	interceptor := ratelimit.UnaryInterceptor(lim)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-client-id", "test-client"))
	info := &grpc.UnaryServerInfo{FullMethod: "/echomap.v1.EchoMap/FetchChallenge"}

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	}

	resp, err := interceptor(ctx, nil, info, handler)
	if err != nil {
		t.Errorf("should pass within limit: %v", err)
	}
	if resp != "ok" {
		t.Error("should return handler response")
	}
}

func TestInterceptor_RejectsOverLimit(t *testing.T) {
	lim := ratelimit.New(1, time.Second) // 1 per second
	interceptor := ratelimit.UnaryInterceptor(lim)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-client-id", "test-client"))
	info := &grpc.UnaryServerInfo{FullMethod: "/echomap.v1.EchoMap/FetchChallenge"}

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	}

	// First request passes
	_, err := interceptor(ctx, nil, info, handler)
	if err != nil {
		t.Fatalf("first request should pass: %v", err)
	}

	// Second request should be rate limited
	_, err = interceptor(ctx, nil, info, handler)
	if err == nil {
		t.Fatal("second request should be rate limited")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error should be a gRPC status: %v", err)
	}
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted, got %s", st.Code())
	}
}

func TestInterceptor_UsesIPAsFallbackKey(t *testing.T) {
	lim := ratelimit.New(1, time.Second)
	interceptor := ratelimit.UnaryInterceptor(lim)

	// No x-client-id metadata — should fall back to peer address or "unknown"
	ctx := context.Background()
	info := &grpc.UnaryServerInfo{FullMethod: "/echomap.v1.EchoMap/FetchChallenge"}

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	}

	// First request passes even without client ID
	_, err := interceptor(ctx, nil, info, handler)
	if err != nil {
		t.Errorf("should work without client ID metadata: %v", err)
	}
}
