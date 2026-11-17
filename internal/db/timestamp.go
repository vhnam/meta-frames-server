package db

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Timestamp reads a database timestamp; a NULL becomes the zero time.
func Timestamp(timestamp pgtype.Timestamptz) time.Time {
	if !timestamp.Valid {
		return time.Time{}
	}
	return timestamp.Time
}
