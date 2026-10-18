package store

import (
	"context"
	"time"
)

type BackupRun struct {
	ID         int64      `db:"id" json:"id"`
	StartedAt  time.Time  `db:"started_at" json:"started_at"`
	FinishedAt *time.Time `db:"finished_at" json:"finished_at"`
	Manual     bool       `db:"manual" json:"manual"`
	OK         bool       `db:"ok" json:"ok"`
	Size       int64      `db:"size" json:"size"`
	SentTo     int        `db:"sent_to" json:"sent_to"`
	Error      string     `db:"error" json:"error"`
}

func StartBackupRun(ctx context.Context, db DB, manual bool) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, `INSERT INTO backup_runs (manual) VALUES ($1) RETURNING id`, manual).Scan(&id)
	return id, err
}

func FinishBackupRun(ctx context.Context, db DB, id int64, ok bool, size int64, sentTo int, errText string) error {
	_, err := db.Exec(ctx, `UPDATE backup_runs SET finished_at=now(), ok=$2, size=$3, sent_to=$4, error=$5 WHERE id=$1`, id, ok, size, sentTo, errText)
	return err
}

// LastBackupOK is when the latest successful copy was made (zero if none).
func LastBackupOK(ctx context.Context, db DB) (time.Time, error) {
	var t *time.Time
	err := db.QueryRow(ctx, `SELECT max(started_at) FROM backup_runs WHERE ok`).Scan(&t)
	if t == nil {
		return time.Time{}, err
	}
	return *t, err
}

// LastBackupAttempt is when the latest automatic attempt started, successful or not (zero if none).
func LastBackupAttempt(ctx context.Context, db DB) (time.Time, error) {
	var t *time.Time
	err := db.QueryRow(ctx, `SELECT max(started_at) FROM backup_runs WHERE NOT manual`).Scan(&t)
	if t == nil {
		return time.Time{}, err
	}
	return *t, err
}

func BackupRuns(ctx context.Context, db DB, limit int) ([]BackupRun, error) {
	return many[BackupRun](db.Query(ctx, `SELECT * FROM backup_runs ORDER BY started_at DESC LIMIT $1`, limit))
}
