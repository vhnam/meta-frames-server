// Package shared has small helpers used by several service packages.
package shared

import (
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
)

// NotFoundOr turns a "no rows" database error into a not-found application error
// and passes any other error through unchanged.
func NotFoundOr(err error, what string) error {
	if db.IsNoRows(err) {
		return apperror.NotFound(what)
	}
	return err
}

// RemoveDuplicates keeps the first occurrence of each id.
func RemoveDuplicates[Value comparable](values []Value) []Value {
	seen := make(map[Value]bool, len(values))
	unique := make([]Value, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	return unique
}

// DateOrDefault returns the supplied date or the fallback when none was given.
func DateOrDefault(date *time.Time, fallback time.Time) time.Time {
	if date == nil {
		return fallback
	}
	return *date
}

// LensIDSet indexes lenses by id.
func LensIDSet(lenses []gen.Lens) map[uuid.UUID]bool {
	set := make(map[uuid.UUID]bool, len(lenses))
	for _, lens := range lenses {
		set[lens.ID] = true
	}
	return set
}
