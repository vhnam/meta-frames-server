package stats

import (
	"context"
	"errors"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestFoldTimelineGroupsMonthsByYear(test *testing.T) {
	rows := []gen.StatsTimelineRow{
		{Year: 2025, Month: 1, Rolls: 2},
		{Year: 2025, Month: 12, Rolls: 1},
		{Year: 2026, Month: 6, Rolls: 4},
	}
	timelines := foldTimeline(rows)
	if len(timelines) != 2 {
		test.Fatalf("years = %d, want 2", len(timelines))
	}
	if timelines[0].Year != 2025 || timelines[0].Months[0] != 2 || timelines[0].Months[11] != 1 || timelines[0].Months[5] != 0 {
		test.Fatalf("2025 = %+v", timelines[0])
	}
	if timelines[1].Year != 2026 || timelines[1].Months[5] != 4 {
		test.Fatalf("2026 = %+v", timelines[1])
	}
}

func TestFoldTimelineWithNoRowsIsEmptyNotNil(test *testing.T) {
	if got := foldTimeline(nil); got == nil || len(got) != 0 {
		test.Fatalf("foldTimeline(nil) = %#v", got)
	}
}

func TestFoldSpendingMergesFilmAndProcessingPerMonth(test *testing.T) {
	film := []gen.StatsFilmSpendRow{
		{Year: 2026, Month: 1, Cost: 300000, Missing: 0},
		{Year: 2026, Month: 2, Cost: 150000, Missing: 1},
		{Year: 2025, Month: 12, Cost: 100000, Missing: 0},
	}
	processing := []gen.StatsProcessingSpendRow{
		{Year: 2026, Month: 1, Cost: 90000, Missing: 0},
		{Year: 2026, Month: 3, Cost: 50000, Missing: 0},
	}
	years := foldSpending(film, processing)

	if len(years) != 2 || years[0].Year != 2025 || years[1].Year != 2026 {
		test.Fatalf("years = %+v", years)
	}
	if years[0].FilmCost != 100000 || years[0].Incomplete {
		test.Fatalf("2025 = %+v", years[0])
	}
	year := years[1]
	if year.FilmCost != 450000 || year.ProcessingCost != 140000 || !year.Incomplete {
		test.Fatalf("2026 totals = %+v", year)
	}
	months := make([]int, len(year.Months))
	for index, month := range year.Months {
		months[index] = month.Month
	}
	if !reflect.DeepEqual(months, []int{1, 2, 3}) {
		test.Fatalf("months = %v", months)
	}
	if january := year.Months[0]; january.FilmCost != 300000 || january.ProcessingCost != 90000 || january.Incomplete {
		test.Fatalf("january = %+v", january)
	}
	if !year.Months[1].Incomplete || year.Months[2].Incomplete {
		test.Fatalf("incomplete flags = %+v", year.Months)
	}
}

func TestFoldSpendingWithNoRows(test *testing.T) {
	if got := foldSpending(nil, nil); got == nil || len(got) != 0 {
		test.Fatalf("foldSpending(nil, nil) = %#v", got)
	}
}

type statsQueries struct {
	gen.Querier
	fail bool
}

func (queries *statsQueries) err() error {
	if queries.fail {
		return errBoom
	}
	return nil
}

func (queries *statsQueries) StatsCameras(_ context.Context, arg gen.StatsCamerasParams) ([]gen.StatsCamerasRow, error) {
	return []gen.StatsCamerasRow{{ID: uuid.New(), Label: "Nikon FM2", Rolls: 3}}, queries.err()
}

func (queries *statsQueries) StatsLenses(_ context.Context, arg gen.StatsLensesParams) ([]gen.StatsLensesRow, error) {
	return []gen.StatsLensesRow{{ID: uuid.New(), Label: "50mm", Rolls: 2}}, queries.err()
}

func (queries *statsQueries) StatsFilm(_ context.Context, _ uuid.UUID) ([]gen.StatsFilmRow, error) {
	return []gen.StatsFilmRow{{Label: "Kodak Gold", Rolls: 4}}, queries.err()
}

func (queries *statsQueries) StatsFilmByBase(_ context.Context, _ uuid.UUID) ([]gen.StatsFilmByBaseRow, error) {
	return []gen.StatsFilmByBaseRow{{Label: "Kodak", Rolls: 6}}, queries.err()
}

func (queries *statsQueries) StatsTimeline(_ context.Context, _ uuid.UUID) ([]gen.StatsTimelineRow, error) {
	return []gen.StatsTimelineRow{{Year: 2026, Month: 1, Rolls: 2}, {Year: 2026, Month: 3, Rolls: 1}}, queries.err()
}

func (queries *statsQueries) StatsFilmSpend(_ context.Context, _ uuid.UUID) ([]gen.StatsFilmSpendRow, error) {
	return []gen.StatsFilmSpendRow{{Year: 2026, Month: 1, Cost: 100, Missing: 1}}, queries.err()
}

func (queries *statsQueries) StatsProcessingSpend(_ context.Context, _ uuid.UUID) ([]gen.StatsProcessingSpendRow, error) {
	return []gen.StatsProcessingSpendRow{{Year: 2026, Month: 1, Cost: 50}, {Year: 2025, Month: 12, Cost: 10}}, queries.err()
}

func newStatsService(fail bool) *Service {
	return New(testutil.Store{Querier: &statsQueries{fail: fail}})
}

func TestStatsGearRanksCamerasAndLenses(test *testing.T) {
	year := 2026
	stats, err := newStatsService(false).Gear(context.Background(), &year)
	if err != nil || len(stats.Cameras) != 1 || stats.Cameras[0].Label != "Nikon FM2" || stats.Lenses[0].Rolls != 2 {
		test.Fatalf("stats=%+v err=%v", stats, err)
	}
}

func TestStatsFilmGroupsByBaseOnRequest(test *testing.T) {
	service := newStatsService(false)
	plain, err := service.Film(context.Background(), false)
	if err != nil || plain[0].Label != "Kodak Gold" {
		test.Fatalf("plain=%+v err=%v", plain, err)
	}
	grouped, err := service.Film(context.Background(), true)
	if err != nil || grouped[0].Label != "Kodak" {
		test.Fatalf("grouped=%+v err=%v", grouped, err)
	}
}

func TestStatsTimelineFoldsMonthsIntoYears(test *testing.T) {
	timelines, err := newStatsService(false).Timeline(context.Background())
	if err != nil || len(timelines) != 1 || timelines[0].Months[0] != 2 || timelines[0].Months[2] != 1 {
		test.Fatalf("timelines=%+v err=%v", timelines, err)
	}
}

func TestStatsSpendingMergesFilmAndProcessingAndFlagsMissingPrices(test *testing.T) {
	years, err := newStatsService(false).Spending(context.Background())
	if err != nil || len(years) != 2 || years[0].Year != 2025 {
		test.Fatalf("years=%+v err=%v", years, err)
	}
	january := years[1].Months[0]
	if january.FilmCost != 100 || january.ProcessingCost != 50 || !january.Incomplete || !years[1].Incomplete {
		test.Fatalf("january=%+v year=%+v", january, years[1])
	}
	if years[0].Incomplete {
		test.Fatal("2025 has no missing prices")
	}
}

func TestStatsPropagateStoreErrors(test *testing.T) {
	service := newStatsService(true)
	ctx := context.Background()
	year := 2026
	checks := map[string]func() error{
		"gear":     func() error { _, err := service.Gear(ctx, &year); return err },
		"film":     func() error { _, err := service.Film(ctx, false); return err },
		"filmBase": func() error { _, err := service.Film(ctx, true); return err },
		"timeline": func() error { _, err := service.Timeline(ctx); return err },
		"spending": func() error { _, err := service.Spending(ctx); return err },
	}
	for name, check := range checks {
		if err := check(); !errors.Is(err, errBoom) {
			test.Errorf("%s: err = %v", name, err)
		}
	}
}
