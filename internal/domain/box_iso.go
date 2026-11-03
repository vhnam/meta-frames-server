package domain

import (
	"regexp"
	"strconv"
	"strings"
)

var boxISOPattern = regexp.MustCompile(`^[1-9][0-9]{0,5}(-[1-9][0-9]{0,5})?$`)

// ValidBoxISO reports whether a box speed is a single ISO ("400") or a rising range ("100-400").
func ValidBoxISO(boxISO string) bool {
	if !boxISOPattern.MatchString(boxISO) {
		return false
	}
	low, high, isRange := strings.Cut(boxISO, "-")
	if !isRange {
		return true
	}
	lowISO, lowErr := strconv.Atoi(low)
	highISO, highErr := strconv.Atoi(high)
	return lowErr == nil && highErr == nil && lowISO < highISO
}

// SingleBoxISO returns the speed of a stock with a single box ISO; a range has no single speed.
func SingleBoxISO(boxISO string) (int32, bool) {
	iso, err := strconv.ParseInt(boxISO, 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(iso), true
}
