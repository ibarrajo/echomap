package grpcserver

import (
	"context"
	"fmt"

	"github.com/elninja/echomap/internal/challenge"
	"github.com/elninja/echomap/internal/config"
	"github.com/elninja/echomap/internal/geo"
	echomapv1 "github.com/elninja/echomap/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Handler implements the EchoMap gRPC service.
type Handler struct {
	echomapv1.UnimplementedEchoMapServer
	cfg    config.Config
	mgr    *challenge.Manager
	engine *geo.Engine
}

// NewHandler creates a new gRPC handler.
func NewHandler(cfg config.Config, mgr *challenge.Manager, engine *geo.Engine) *Handler {
	return &Handler{cfg: cfg, mgr: mgr, engine: engine}
}

// FetchChallenge generates a new challenge with probe targets.
func (h *Handler) FetchChallenge(ctx context.Context, req *echomapv1.ChallengeRequest) (*echomapv1.ChallengeResponse, error) {
	if req.ClientId == "" {
		return nil, status.Error(codes.InvalidArgument, "client_id is required")
	}

	tok, err := h.mgr.GenerateToken(req.ClientId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "generate token: %v", err)
	}

	// For now, use a default estimated location (0,0) — in production,
	// this would come from IP geolocation of the client.
	probes := h.mgr.SelectProbes(0, 0, h.cfg.ProbeCount)

	targets := make([]*echomapv1.ProbeTarget, len(probes))
	for i, p := range probes {
		targets[i] = &echomapv1.ProbeTarget{
			Id:        p.ID,
			Host:      p.Host,
			Port:      int32(p.Port),
			PingCount: int32(h.cfg.PingCount),
		}
	}

	return &echomapv1.ChallengeResponse{
		ChallengeId: tok.ChallengeID,
		Token:       tok.Token,
		Targets:     targets,
		TimeoutMs:   int32(h.cfg.TimeoutMS),
		ExpiresAt:   tok.ExpiresAt,
	}, nil
}

// SubmitMeasurement validates a challenge response and computes geolocation.
func (h *Handler) SubmitMeasurement(ctx context.Context, req *echomapv1.MeasurementRequest) (*echomapv1.MeasurementResponse, error) {
	// Validate token
	if err := h.mgr.ValidateToken(req.ChallengeId, req.Token); err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid challenge: %v", err)
	}

	// Convert proto measurements to geo measurements
	probeMap := h.buildProbeMap()
	var measurements []geo.Measurement
	for _, pm := range req.Measurements {
		probe, ok := probeMap[pm.ProbeId]
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "unknown probe: %s", pm.ProbeId)
		}
		rtts := make([]int, len(pm.RttsUs))
		for i, r := range pm.RttsUs {
			rtts[i] = int(r)
		}
		measurements = append(measurements, geo.Measurement{
			ProbeID:  pm.ProbeId,
			ProbeLat: probe.Lat,
			ProbeLon: probe.Lon,
			RTTs:     rtts,
		})
	}

	// Run geolocation engine
	result := h.engine.Locate(measurements)

	// Build response
	probeResults := make([]*echomapv1.ProbeResult, len(result.ProbeResults))
	for i, pr := range result.ProbeResults {
		probeResults[i] = &echomapv1.ProbeResult{
			ProbeId:       pr.ProbeID,
			RttMs:         pr.RTTMS,
			JitterMs:      pr.JitterMS,
			MaxDistanceKm: pr.MaxDistanceKM,
		}
	}

	exclusions := make([]*echomapv1.Exclusion, len(result.Exclusions))
	for i, e := range result.Exclusions {
		exclusions[i] = &echomapv1.Exclusion{
			Region:     e.Region,
			Confidence: e.Confidence,
		}
	}

	verdictStatus := echomapv1.Status_STATUS_PLAUSIBLE
	if result.Confidence >= 0.85 {
		verdictStatus = echomapv1.Status_STATUS_CONFIRMED
	} else if result.Spoofing.JitterAbnormal || result.Spoofing.RatioInconsistent {
		verdictStatus = echomapv1.Status_STATUS_SUSPICIOUS
	} else if result.Spoofing.PhysicallyImpossible {
		verdictStatus = echomapv1.Status_STATUS_REJECTED
	}

	return &echomapv1.MeasurementResponse{
		Verdict: &echomapv1.Verdict{
			Status:     verdictStatus,
			Confidence: result.Confidence,
		},
		Region: &echomapv1.Region{
			Lat:      result.Region.Lat,
			Lon:      result.Region.Lon,
			RadiusKm: result.Region.RadiusKM,
			Label:    fmt.Sprintf("%.1f, %.1f (±%.0f km)", result.Region.Lat, result.Region.Lon, result.Region.RadiusKM),
		},
		Exclusions:   exclusions,
		ProbeResults: probeResults,
		Spoofing: &echomapv1.SpoofingIndicators{
			VpnLikely:           result.Spoofing.VPNLikely,
			JitterAbnormal:      result.Spoofing.JitterAbnormal,
			RatioInconsistent:   result.Spoofing.RatioInconsistent,
			PhysicallyImpossible: result.Spoofing.PhysicallyImpossible,
		},
	}, nil
}

// buildProbeMap returns a map of probe ID → probe for coordinate lookup.
func (h *Handler) buildProbeMap() map[string]challenge.Probe {
	// Get all known probes by selecting a large count from origin
	allProbes := h.mgr.SelectProbes(0, 0, 100)
	m := make(map[string]challenge.Probe, len(allProbes))
	for _, p := range allProbes {
		m[p.ID] = p
	}
	return m
}
