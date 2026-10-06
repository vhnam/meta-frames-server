package convert

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"meta-frames-server/internal/api"
)

func ToAPIDate(date *time.Time) *openapi_types.Date {
	if date == nil {
		return nil
	}
	return &openapi_types.Date{Time: *date}
}

func FromAPIDate(date *openapi_types.Date) *time.Time {
	if date == nil {
		return nil
	}
	return &date.Time
}

func APIDate(date time.Time) openapi_types.Date { return openapi_types.Date{Time: date} }

// OptionalDate reads the date of an optional {date} request body.
func OptionalDate(body *api.DateInput) *time.Time {
	if body == nil {
		return nil
	}
	return FromAPIDate(body.Date)
}

// Timestamp reads a database timestamp; a NULL becomes the zero time.
func Timestamp(timestamp pgtype.Timestamptz) time.Time {
	if !timestamp.Valid {
		return time.Time{}
	}
	return timestamp.Time
}
