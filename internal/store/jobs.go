package store

import (
	"context"
	"database/sql"
	"time"

	"luduslog/internal/models"
)

func (s *Store) Enqueue(ctx context.Context, kind, payload string, runAt time.Time) error {
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO jobs(kind,payload,run_at,attempts,created_at) VALUES(?,?,?,0,?)`,
		kind, payload, runAt.UTC().Format(time.RFC3339), Now())
	return err
}

func (s *Store) ClaimJob(ctx context.Context) (*models.Job, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var j models.Job
	var runAt, payload string
	err = tx.QueryRowContext(ctx, `SELECT id,kind,payload,run_at,attempts,COALESCE(last_error,'') FROM jobs
		WHERE run_at<=? ORDER BY run_at LIMIT 1`, Now()).Scan(&j.ID, &j.Kind, &payload, &runAt, &j.Attempts, &j.LastErr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j.Payload = payload
	j.RunAt = parseTime(runAt)
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET attempts=attempts+1,run_at=? WHERE id=?`,
		time.Now().UTC().Add(10*time.Minute).Format(time.RFC3339), j.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Store) FinishJob(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM jobs WHERE id=?`, id)
	return err
}

func (s *Store) RetryJob(ctx context.Context, id int64, errMsg string, backoff time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET run_at=?,last_error=? WHERE id=?`,
		time.Now().UTC().Add(backoff).Format(time.RFC3339), errMsg, id)
	return err
}

func (s *Store) Jobs(ctx context.Context, limit int) ([]models.Job, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,kind,payload,run_at,attempts,COALESCE(last_error,'') FROM jobs ORDER BY run_at LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Job
	for rows.Next() {
		var j models.Job
		var runAt string
		rows.Scan(&j.ID, &j.Kind, &j.Payload, &runAt, &j.Attempts, &j.LastErr)
		j.RunAt = parseTime(runAt)
		out = append(out, j)
	}
	return out, rows.Err()
}
