package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type File struct {
	ID        uuid.UUID `db:"id" json:"id"`
	Name      string    `db:"name" json:"name"`
	Mime      string    `db:"mime" json:"mime"`
	Size      int       `db:"size" json:"size"`
	Data      []byte    `db:"data" json:"-"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

func PutFile(ctx context.Context, db DB, name, mime string, data []byte) (*File, error) {
	return one[File](db.Query(ctx, `INSERT INTO files (name, mime, size, data) VALUES ($1,$2,$3,$4)
		RETURNING id, name, mime, size, ''::bytea AS data, created_at`, name, mime, len(data), data))
}

func GetFile(ctx context.Context, db DB, id uuid.UUID) (*File, error) {
	return one[File](db.Query(ctx, `SELECT * FROM files WHERE id=$1`, id))
}

func ListFiles(ctx context.Context, db DB) ([]File, error) {
	return many[File](db.Query(ctx, `SELECT id, name, mime, size, ''::bytea AS data, created_at FROM files ORDER BY created_at DESC`))
}

func DeleteFile(ctx context.Context, db DB, id uuid.UUID) error {
	_, err := db.Exec(ctx, `DELETE FROM files WHERE id=$1`, id)
	return err
}
