package ratelimit

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Limiter implements a sliding window rate limiter per key.
type Limiter struct {
	maxRequests int
	window      time.Duration
	mu          sync.Mutex
	clients     map[string]*clientWindow
}

type clientWindow struct {
	count    int
	windowAt time.Time
}

// New creates a new Limiter.
func New(maxRequests int, window time.Duration) *Limiter {
	return &Limiter{
		maxRequests: maxRequests,
		window:      window,
		clients:     make(map[string]*clientWindow),
	}
}

// Allow checks if a request from the given key is allowed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	cw, exists := l.clients[key]
	if !exists || now.Sub(cw.windowAt) >= l.window {
		// New window
		l.clients[key] = &clientWindow{count: 1, windowAt: now}
		return true
	}

	if cw.count >= l.maxRequests {
		return false
	}

	cw.count++
	return true
}

// UnaryInterceptor returns a gRPC unary server interceptor that rate-limits by client ID.
func UnaryInterceptor(lim *Limiter) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		key := extractClientKey(ctx)

		if !lim.Allow(key) {
			return nil, status.Errorf(codes.ResourceExhausted,
				"rate limit exceeded: max %d requests per %s", lim.maxRequests, lim.window)
		}

		return handler(ctx, req)
	}
}

func extractClientKey(ctx context.Context) string {
	// Try x-client-id from metadata first
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if ids := md.Get("x-client-id"); len(ids) > 0 {
			return ids[0]
		}
	}

	// Fall back to peer address
	if p, ok := peer.FromContext(ctx); ok {
		return p.Addr.String()
	}

	return "unknown"
}
