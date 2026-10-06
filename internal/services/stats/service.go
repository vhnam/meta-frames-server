package stats

import (
	"context"
	"sort"

	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/services/roll"
	"meta-frames-server/internal/services/scan"
)

// Service answers the search and statistics use cases (UC-35 to UC-39).
type Service struct {
	store db.Store
}

func New(store db.Store) *Service { return &Service{store: store} }

// SearchByFocalLength finds rolls shot with a lens of the given focal length, with their scans (UC-35).
func (service *Service) SearchByFocalLength(ctx context.Context, focalLength int) ([]SearchHit, error) {
	queries := service.store.Queries()
	rolls, err := roll.ListSummaries(ctx, queries, gen.ListRollSummariesParams{FocalLength: pointers.To(int32(focalLength))})
	if err != nil {
		return nil, err
	}
	hits := make([]SearchHit, len(rolls))
	for index, summary := range rolls {
		scans, err := queries.ListRollScans(ctx, summary.ID)
		if err != nil {
			return nil, err
		}
		refs := make([]scan.RefView, len(scans))
		for scanIndex, row := range scans {
			refs[scanIndex] = scan.RefView{ID: row.ID, ProcessingID: row.ProcessingID, FrameNumber: row.FrameNumber, Scanner: row.Scanner}
		}
		hits[index] = SearchHit{Roll: summary, Scans: refs}
	}
	return hits, nil
}

// Gear ranks cameras and lenses by rolls shot, for all time or one year (UC-36).
func (service *Service) Gear(ctx context.Context, year *int) (GearStats, error) {
	queries := service.store.Queries()
	cameras, err := queries.StatsCameras(ctx, pointers.Int32(year))
	if err != nil {
		return GearStats{}, err
	}
	lenses, err := queries.StatsLenses(ctx, pointers.Int32(year))
	if err != nil {
		return GearStats{}, err
	}
	stats := GearStats{Cameras: make([]RankedItem, len(cameras)), Lenses: make([]RankedItem, len(lenses))}
	for index, row := range cameras {
		stats.Cameras[index] = RankedItem{ID: row.ID, Label: row.Label, Rolls: row.Rolls}
	}
	for index, row := range lenses {
		stats.Lenses[index] = RankedItem{ID: row.ID, Label: row.Label, Rolls: row.Rolls}
	}
	return stats, nil
}

// Film ranks film stocks by rolls shot, optionally merging repacks by base stock (UC-37).
func (service *Service) Film(ctx context.Context, groupByBase bool) ([]RankedItem, error) {
	queries := service.store.Queries()
	if groupByBase {
		rows, err := queries.StatsFilmByBase(ctx)
		if err != nil {
			return nil, err
		}
		items := make([]RankedItem, len(rows))
		for index, row := range rows {
			items[index] = RankedItem{ID: row.ID, Label: row.Label, Rolls: row.Rolls}
		}
		return items, nil
	}
	rows, err := queries.StatsFilm(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]RankedItem, len(rows))
	for index, row := range rows {
		items[index] = RankedItem{ID: row.ID, Label: row.Label, Rolls: row.Rolls}
	}
	return items, nil
}

// Timeline counts rolls started per month for each year (UC-38).
func (service *Service) Timeline(ctx context.Context) ([]YearTimeline, error) {
	rows, err := service.store.Queries().StatsTimeline(ctx)
	if err != nil {
		return nil, err
	}
	return foldTimeline(rows), nil
}

func foldTimeline(rows []gen.StatsTimelineRow) []YearTimeline {
	timelines := []YearTimeline{}
	indexByYear := map[int]int{}
	for _, row := range rows {
		index, seen := indexByYear[int(row.Year)]
		if !seen {
			index = len(timelines)
			indexByYear[int(row.Year)] = index
			timelines = append(timelines, YearTimeline{Year: int(row.Year)})
		}
		timelines[index].Months[row.Month-1] = int(row.Rolls)
	}
	return timelines
}

// Spending sums film and processing cost per month and year (UC-39).
// Film cost is dated by when the roll was recorded; processing cost by its sent date.
func (service *Service) Spending(ctx context.Context) ([]YearSpend, error) {
	queries := service.store.Queries()
	film, err := queries.StatsFilmSpend(ctx)
	if err != nil {
		return nil, err
	}
	processing, err := queries.StatsProcessingSpend(ctx)
	if err != nil {
		return nil, err
	}
	return foldSpending(film, processing), nil
}

// foldSpending merges film and processing rows into year totals with monthly breakdowns.
// Missing prices count as zero but mark the bucket incomplete.
func foldSpending(film []gen.StatsFilmSpendRow, processing []gen.StatsProcessingSpendRow) []YearSpend {
	type monthKey struct{ year, month int }
	buckets := map[monthKey]*SpendBucket{}
	bucketFor := func(year, month int32) *SpendBucket {
		key := monthKey{int(year), int(month)}
		if bucket, exists := buckets[key]; exists {
			return bucket
		}
		bucket := &SpendBucket{}
		buckets[key] = bucket
		return bucket
	}
	for _, row := range film {
		bucket := bucketFor(row.Year, row.Month)
		bucket.FilmCost += int(row.Cost)
		bucket.Incomplete = bucket.Incomplete || row.Missing > 0
	}
	for _, row := range processing {
		bucket := bucketFor(row.Year, row.Month)
		bucket.ProcessingCost += int(row.Cost)
		bucket.Incomplete = bucket.Incomplete || row.Missing > 0
	}

	keys := make([]monthKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].year != keys[right].year {
			return keys[left].year < keys[right].year
		}
		return keys[left].month < keys[right].month
	})

	years := []YearSpend{}
	for _, key := range keys {
		if len(years) == 0 || years[len(years)-1].Year != key.year {
			years = append(years, YearSpend{Year: key.year, Months: []MonthSpend{}})
		}
		year := &years[len(years)-1]
		bucket := *buckets[key]
		year.Months = append(year.Months, MonthSpend{Month: key.month, SpendBucket: bucket})
		year.FilmCost += bucket.FilmCost
		year.ProcessingCost += bucket.ProcessingCost
		year.Incomplete = year.Incomplete || bucket.Incomplete
	}
	return years
}
