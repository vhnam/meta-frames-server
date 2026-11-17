package api

import (
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
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
func OptionalDate(body *DateInput) *time.Time {
	if body == nil {
		return nil
	}
	return FromAPIDate(body.Date)
}
