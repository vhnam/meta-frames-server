// Package shared has small helpers used by several service packages.
package shared

import (
	"strings"
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

// NoteEdit is what a compare-and-set edit of a free-text note should do.
type NoteEdit int

const (
	// NoteApply writes the desired text.
	NoteApply NoteEdit = iota
	// NoteUnchanged writes nothing: the desired text is already stored (a replay).
	NoteUnchanged
	// NoteConflict writes nothing: the text changed since the edit's base.
	NoteConflict
)

// DecideNoteEdit compares trimmed text, with a missing note equal to "". Without a base the edit
// overwrites whatever is stored. The caller holds the row lock of the note's roll, so the
// decision and the write it leads to happen in one transaction.
func DecideNoteEdit(current, desired, base *string) NoteEdit {
	if base == nil {
		return NoteApply
	}
	stored := trimmedText(current)
	if stored == trimmedText(desired) {
		return NoteUnchanged
	}
	if stored == trimmedText(base) {
		return NoteApply
	}
	return NoteConflict
}

func trimmedText(text *string) string {
	if text == nil {
		return ""
	}
	return strings.TrimSpace(*text)
}
