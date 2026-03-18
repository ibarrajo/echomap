package grpcserver

import (
	"context"
	"time"

	"github.com/elninja/echomap/internal/challenge"
	"github.com/elninja/echomap/internal/config"
	"github.com/elninja/echomap/internal/geo"
	"github.com/elninja/echomap/internal/storage"
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
	store  *storage.Repository // nil if no persistence configured
}

// NewHandler creates a new gRPC handler.
func NewHandler(cfg config.Config, mgr *challenge.Manager, engine *geo.Engine) *Handler {
	return &Handler{cfg: cfg, mgr: mgr, engine: engine}
}

// WithStorage sets the storage backend for persisting results.
func (h *Handler) WithStorage(store *storage.Repository) *Handler {
	h.store = store
	return h
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

	// Use all available probes for maximum triangulation accuracy.
	// With only ~11 global probes, selecting a subset risks missing the user's region.
	probes := h.mgr.SelectProbes(0, 0, 100)

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
	clientID := h.mgr.GetClientID(req.ChallengeId)
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

	// Enhanced spoofing detection
	vpnDetected := geo.DetectVPN(measurements)
	correlation := geo.CorrelateProbes(measurements)

	if vpnDetected {
		result.Spoofing.VPNLikely = true
	}
	if !correlation.Consistent {
		result.Spoofing.RatioInconsistent = true
	}

	// Build response
	probeResults := make([]*echomapv1.ProbeResult, len(result.ProbeResults))
	for i, pr := range result.ProbeResults {
		probeResults[i] = &echomapv1.ProbeResult{
			ProbeId:         pr.ProbeID,
			RttMs:           pr.RTTMS,
			JitterMs:        pr.JitterMS,
			MaxDistanceKm:   pr.MaxDistanceKM,
			DatasetExpectedMs: pr.DatasetExpectedMS,
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
	if result.Spoofing.PhysicallyImpossible {
		verdictStatus = echomapv1.Status_STATUS_REJECTED
	} else if result.Spoofing.VPNLikely || result.Spoofing.JitterAbnormal || result.Spoofing.RatioInconsistent {
		verdictStatus = echomapv1.Status_STATUS_SUSPICIOUS
	} else if result.Confidence >= 0.85 {
		verdictStatus = echomapv1.Status_STATUS_CONFIRMED
	}

	resp := &echomapv1.MeasurementResponse{
		Verdict: &echomapv1.Verdict{
			Status:     verdictStatus,
			Confidence: result.Confidence,
		},
		Region: &echomapv1.Region{
			Lat:      result.Region.Lat,
			Lon:      result.Region.Lon,
			RadiusKm: result.Region.RadiusKM,
			Label:    geo.RegionLabel(result.Region.Lat, result.Region.Lon, result.Region.RadiusKM),
		},
		Exclusions:   exclusions,
		ProbeResults: probeResults,
		Spoofing: &echomapv1.SpoofingIndicators{
			VpnLikely:            vpnDetected,
			JitterAbnormal:       result.Spoofing.JitterAbnormal,
			RatioInconsistent:    !correlation.Consistent || result.Spoofing.RatioInconsistent,
			PhysicallyImpossible: result.Spoofing.PhysicallyImpossible,
		},
	}

	// Persist result + anomalies if storage is configured
	if h.store != nil {
		matchedCity := ""
		matchError := 0.0
		if result.DatasetMatch != nil {
			matchedCity = result.DatasetMatch.City
			matchError = result.DatasetMatch.Error
		}

		suspicious := vpnDetected || result.Spoofing.JitterAbnormal || !correlation.Consistent
		_ = h.store.SaveResult(ctx, storage.MeasurementRecord{
			ChallengeID: req.ChallengeId,
			ClientID:    clientID,
			Status:      verdictStatus.String(),
			Confidence:  result.Confidence,
			RegionLat:   result.Region.Lat,
			RegionLon:   result.Region.Lon,
			RadiusKM:    result.Region.RadiusKM,
			RegionLabel: resp.Region.Label,
			MatchedCity: matchedCity,
			MatchError:  matchError,
			Suspicious:  suspicious,
			CreatedAt:   time.Now(),
		})

		if result.Spoofing.JitterAbnormal {
			_ = h.store.LogAnomaly(ctx, storage.AnomalyRecord{
				ChallengeID: req.ChallengeId, ClientID: clientID,
				Type: "JITTER_ABNORMAL", Details: "abnormal jitter pattern detected",
				CreatedAt: time.Now(),
			})
		}
		if vpnDetected {
			_ = h.store.LogAnomaly(ctx, storage.AnomalyRecord{
				ChallengeID: req.ChallengeId, ClientID: clientID,
				Type: "VPN_DETECTED", Details: "all probes show similar high RTTs with low jitter",
				CreatedAt: time.Now(),
			})
		}
		if !correlation.Consistent {
			_ = h.store.LogAnomaly(ctx, storage.AnomalyRecord{
				ChallengeID: req.ChallengeId, ClientID: clientID,
				Type: "RATIO_INCONSISTENT", Details: "probe RTT ratios violate triangle inequality",
				CreatedAt: time.Now(),
			})
		}
	}

	return resp, nil
}

// buildProbeMap returns a map of probe ID → probe for coordinate lookup.
func (h *Handler) buildProbeMap() map[string]challenge.Probe {
	allProbes := h.mgr.SelectProbes(0, 0, 100)
	m := make(map[string]challenge.Probe, len(allProbes))
	for _, p := range allProbes {
		m[p.ID] = p
	}
	return m
}
