package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elninja/echomap/internal/storage"
)

func newTestRepo(t *testing.T) *storage.Repository {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

// --- Save & Get Result ---

func TestSaveResult_AndGetByID(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	result := storage.MeasurementRecord{
		ChallengeID: "ch-123",
		ClientID:    "client-abc",
		Status:      "CONFIRMED",
		Confidence:  0.92,
		RegionLat:   52.37,
		RegionLon:   4.90,
		RadiusKM:    150,
		RegionLabel: "Amsterdam area",
		MatchedCity: "Amsterdam",
		MatchError:  0.08,
		Suspicious:  false,
		CreatedAt:   time.Now(),
	}

	err := repo.SaveResult(ctx, result)
	if err != nil {
		t.Fatalf("SaveResult: %v", err)
	}

	got, err := repo.GetResult(ctx, "ch-123")
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if got.ClientID != "client-abc" {
		t.Errorf("client_id: got %s, want client-abc", got.ClientID)
	}
	if got.Status != "CONFIRMED" {
		t.Errorf("status: got %s, want CONFIRMED", got.Status)
	}
	if got.MatchedCity != "Amsterdam" {
		t.Errorf("matched_city: got %s, want Amsterdam", got.MatchedCity)
	}
}

func TestGetResult_NotFound(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	_, err := repo.GetResult(ctx, "nonexistent")
	if err == nil {
		t.Error("should return error for nonexistent challenge")
	}
}

// --- Client History ---

func TestGetClientHistory(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	for i, id := range []string{"ch-1", "ch-2", "ch-3"} {
		repo.SaveResult(ctx, storage.MeasurementRecord{
			ChallengeID: id,
			ClientID:    "client-abc",
			Status:      "CONFIRMED",
			Confidence:  0.9,
			RegionLat:   52.37,
			RegionLon:   4.90,
			RadiusKM:    150,
			MatchedCity: "Amsterdam",
			CreatedAt:   time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	history, err := repo.GetClientHistory(ctx, "client-abc", 10)
	if err != nil {
		t.Fatalf("GetClientHistory: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("expected 3 records, got %d", len(history))
	}
}

func TestGetClientHistory_LimitWorks(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		repo.SaveResult(ctx, storage.MeasurementRecord{
			ChallengeID: "ch-" + string(rune('a'+i)),
			ClientID:    "client-xyz",
			Status:      "PLAUSIBLE",
			Confidence:  0.6,
			CreatedAt:   time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	history, err := repo.GetClientHistory(ctx, "client-xyz", 2)
	if err != nil {
		t.Fatalf("GetClientHistory: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("limit 2 should return 2 records, got %d", len(history))
	}
}

func TestGetClientHistory_Empty(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	history, err := repo.GetClientHistory(ctx, "nobody", 10)
	if err != nil {
		t.Fatalf("GetClientHistory: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected 0 records for unknown client, got %d", len(history))
	}
}

// --- Anomaly Logging ---

func TestLogAnomaly_AndQuery(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	err := repo.LogAnomaly(ctx, storage.AnomalyRecord{
		ChallengeID: "ch-999",
		ClientID:    "client-bad",
		Type:        "ZERO_JITTER",
		Details:     "all probes reported identical RTTs",
		CreatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("LogAnomaly: %v", err)
	}

	anomalies, err := repo.GetAnomalies(ctx, "client-bad", 10)
	if err != nil {
		t.Fatalf("GetAnomalies: %v", err)
	}
	if len(anomalies) != 1 {
		t.Fatalf("expected 1 anomaly, got %d", len(anomalies))
	}
	if anomalies[0].Type != "ZERO_JITTER" {
		t.Errorf("type: got %s, want ZERO_JITTER", anomalies[0].Type)
	}
}

func TestLogAnomaly_MultipleTypes(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	for _, typ := range []string{"ZERO_JITTER", "RATIO_MISMATCH", "VPN_DETECTED"} {
		repo.LogAnomaly(ctx, storage.AnomalyRecord{
			ChallengeID: "ch-" + typ,
			ClientID:    "client-bad",
			Type:        typ,
			Details:     "test",
			CreatedAt:   time.Now(),
		})
	}

	anomalies, err := repo.GetAnomalies(ctx, "client-bad", 10)
	if err != nil {
		t.Fatalf("GetAnomalies: %v", err)
	}
	if len(anomalies) != 3 {
		t.Errorf("expected 3 anomalies, got %d", len(anomalies))
	}
}

// --- Recent Suspicious Count ---

func TestCountSuspicious(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	// 2 suspicious, 1 clean
	repo.SaveResult(ctx, storage.MeasurementRecord{
		ChallengeID: "ch-s1", ClientID: "c1", Status: "SUSPICIOUS", Suspicious: true, CreatedAt: time.Now(),
	})
	repo.SaveResult(ctx, storage.MeasurementRecord{
		ChallengeID: "ch-s2", ClientID: "c1", Status: "SUSPICIOUS", Suspicious: true, CreatedAt: time.Now(),
	})
	repo.SaveResult(ctx, storage.MeasurementRecord{
		ChallengeID: "ch-ok", ClientID: "c1", Status: "CONFIRMED", Suspicious: false, CreatedAt: time.Now(),
	})

	count, err := repo.CountSuspicious(ctx, "c1", time.Hour)
	if err != nil {
		t.Fatalf("CountSuspicious: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 suspicious, got %d", count)
	}
}
