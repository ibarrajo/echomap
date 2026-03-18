package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// MeasurementRecord is a persisted geolocation result.
type MeasurementRecord struct {
	ChallengeID string
	ClientID    string
	Status      string
	Confidence  float64
	RegionLat   float64
	RegionLon   float64
	RadiusKM    float64
	RegionLabel string
	MatchedCity string
	MatchError  float64
	Suspicious  bool
	CreatedAt   time.Time
}

// AnomalyRecord is a persisted anomaly log entry.
type AnomalyRecord struct {
	ChallengeID string
	ClientID    string
	Type        string
	Details     string
	CreatedAt   time.Time
}

// Repository handles SQLite persistence.
type Repository struct {
	db *sql.DB
}

// New creates a new Repository and initializes the schema.
func New(dbPath string) (*Repository, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &Repository{db: db}, nil
}

// Close closes the database connection.
func (r *Repository) Close() error {
	return r.db.Close()
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS measurements (
			challenge_id TEXT PRIMARY KEY,
			client_id    TEXT NOT NULL,
			status       TEXT NOT NULL,
			confidence   REAL,
			region_lat   REAL,
			region_lon   REAL,
			radius_km    REAL,
			region_label TEXT,
			matched_city TEXT,
			match_error  REAL,
			suspicious   INTEGER DEFAULT 0,
			created_at   DATETIME NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_measurements_client ON measurements(client_id, created_at);
		CREATE INDEX IF NOT EXISTS idx_measurements_suspicious ON measurements(client_id, suspicious, created_at);

		CREATE TABLE IF NOT EXISTS anomalies (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			challenge_id TEXT NOT NULL,
			client_id    TEXT NOT NULL,
			type         TEXT NOT NULL,
			details      TEXT,
			created_at   DATETIME NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_anomalies_client ON anomalies(client_id, created_at);
	`)
	return err
}

// SaveResult persists a measurement result.
func (r *Repository) SaveResult(ctx context.Context, rec MeasurementRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO measurements (challenge_id, client_id, status, confidence,
			region_lat, region_lon, radius_km, region_label,
			matched_city, match_error, suspicious, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ChallengeID, rec.ClientID, rec.Status, rec.Confidence,
		rec.RegionLat, rec.RegionLon, rec.RadiusKM, rec.RegionLabel,
		rec.MatchedCity, rec.MatchError, boolToInt(rec.Suspicious), rec.CreatedAt,
	)
	return err
}

// GetResult retrieves a measurement result by challenge ID.
func (r *Repository) GetResult(ctx context.Context, challengeID string) (MeasurementRecord, error) {
	var rec MeasurementRecord
	var suspicious int
	err := r.db.QueryRowContext(ctx, `
		SELECT challenge_id, client_id, status, confidence,
			region_lat, region_lon, radius_km, region_label,
			matched_city, match_error, suspicious, created_at
		FROM measurements WHERE challenge_id = ?`, challengeID,
	).Scan(
		&rec.ChallengeID, &rec.ClientID, &rec.Status, &rec.Confidence,
		&rec.RegionLat, &rec.RegionLon, &rec.RadiusKM, &rec.RegionLabel,
		&rec.MatchedCity, &rec.MatchError, &suspicious, &rec.CreatedAt,
	)
	if err != nil {
		return rec, fmt.Errorf("get result: %w", err)
	}
	rec.Suspicious = suspicious != 0
	return rec, nil
}

// GetClientHistory returns recent measurement records for a client, newest first.
func (r *Repository) GetClientHistory(ctx context.Context, clientID string, limit int) ([]MeasurementRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT challenge_id, client_id, status, confidence,
			region_lat, region_lon, radius_km, region_label,
			matched_city, match_error, suspicious, created_at
		FROM measurements
		WHERE client_id = ?
		ORDER BY created_at DESC
		LIMIT ?`, clientID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query client history: %w", err)
	}
	defer rows.Close()

	var results []MeasurementRecord
	for rows.Next() {
		var rec MeasurementRecord
		var suspicious int
		if err := rows.Scan(
			&rec.ChallengeID, &rec.ClientID, &rec.Status, &rec.Confidence,
			&rec.RegionLat, &rec.RegionLon, &rec.RadiusKM, &rec.RegionLabel,
			&rec.MatchedCity, &rec.MatchError, &suspicious, &rec.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		rec.Suspicious = suspicious != 0
		results = append(results, rec)
	}
	return results, rows.Err()
}

// LogAnomaly persists an anomaly record.
func (r *Repository) LogAnomaly(ctx context.Context, rec AnomalyRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO anomalies (challenge_id, client_id, type, details, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		rec.ChallengeID, rec.ClientID, rec.Type, rec.Details, rec.CreatedAt,
	)
	return err
}

// GetAnomalies returns recent anomaly records for a client, newest first.
func (r *Repository) GetAnomalies(ctx context.Context, clientID string, limit int) ([]AnomalyRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT challenge_id, client_id, type, details, created_at
		FROM anomalies
		WHERE client_id = ?
		ORDER BY created_at DESC
		LIMIT ?`, clientID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query anomalies: %w", err)
	}
	defer rows.Close()

	var results []AnomalyRecord
	for rows.Next() {
		var rec AnomalyRecord
		if err := rows.Scan(&rec.ChallengeID, &rec.ClientID, &rec.Type, &rec.Details, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		results = append(results, rec)
	}
	return results, rows.Err()
}

// CountSuspicious returns the number of suspicious results for a client within a time window.
func (r *Repository) CountSuspicious(ctx context.Context, clientID string, window time.Duration) (int, error) {
	since := time.Now().Add(-window)
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM measurements
		WHERE client_id = ? AND suspicious = 1 AND created_at >= ?`,
		clientID, since,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count suspicious: %w", err)
	}
	return count, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
