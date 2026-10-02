package roll

import (
	"sort"
	"time"
)

// expiryEnd is the last day of the expiry month, or of the year when the month is unknown (UC-21).
func expiryEnd(year, month *int32) *time.Time {
	if year == nil {
		return nil
	}
	monthNumber := unknownExpiryMonth
	if month != nil {
		monthNumber = int(*month)
	}
	// Day 0 of the following month is the last day of this one.
	end := time.Date(int(*year), time.Month(monthNumber)+1, 0, 0, 0, 0, 0, time.UTC)
	return &end
}

func isExpired(year, month *int32, today time.Time) bool {
	end := expiryEnd(year, month)
	return end != nil && end.Before(today)
}

// buildExpiryReport lists rolls that are expired or expire within the horizon, soonest first,
// and separately the rolls that have no expiry information (UC-21).
func buildExpiryReport(rolls []Summary, today time.Time) ExpiryReport {
	report := ExpiryReport{Expiring: []ExpiringRoll{}, NoExpiry: []Summary{}}
	horizon := today.AddDate(0, expiryHorizonMonths, 0)
	for _, roll := range rolls {
		end := expiryEnd(roll.ExpiryYear, roll.ExpiryMonth)
		switch {
		case end == nil:
			report.NoExpiry = append(report.NoExpiry, roll)
		case !end.After(horizon):
			report.Expiring = append(report.Expiring, ExpiringRoll{Roll: roll, ExpiresOn: *end, Expired: end.Before(today)})
		}
	}
	sort.SliceStable(report.Expiring, func(left, right int) bool {
		return report.Expiring[left].ExpiresOn.Before(report.Expiring[right].ExpiresOn)
	})
	return report
}
