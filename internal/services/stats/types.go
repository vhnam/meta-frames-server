package stats

import (
	"github.com/google/uuid"

	"meta-frames-server/internal/services/roll"
	"meta-frames-server/internal/services/scan"
)

type SearchHit struct {
	Roll  roll.Summary
	Scans []scan.RefView
}

type RankedItem struct {
	ID    uuid.UUID
	Label string
	Rolls int32
}

type GearStats struct {
	Cameras []RankedItem
	Lenses  []RankedItem
}

type YearTimeline struct {
	Year   int
	Months [12]int
}

type SpendBucket struct {
	FilmCost       int
	ProcessingCost int
	Incomplete     bool
}

type MonthSpend struct {
	Month int
	SpendBucket
}

type YearSpend struct {
	Year int
	SpendBucket
	Months []MonthSpend
}
