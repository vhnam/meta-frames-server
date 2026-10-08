package stats

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	statssvc "meta-frames-server/internal/services/stats"
	"meta-frames-server/internal/testutil"
)

var errBoom = errors.New("boom")

type queries struct {
	gen.Querier
	fail bool
}

func (stub *queries) err() error {
	if stub.fail {
		return errBoom
	}
	return nil
}

func (stub *queries) ListRollSummaries(context.Context, gen.ListRollSummariesParams) ([]gen.ListRollSummariesRow, error) {
	return []gen.ListRollSummariesRow{{ID: uuid.New(), Status: domain.RollStatusScanned}}, stub.err()
}

func (stub *queries) ListRollScans(_ context.Context, arg gen.ListRollScansParams) ([]gen.ListRollScansRow, error) {
	return []gen.ListRollScansRow{{ID: uuid.New(), FrameNumber: 7, Scanner: domain.ScannerNoritsu}}, stub.err()
}

func (stub *queries) StatsCameras(_ context.Context, arg gen.StatsCamerasParams) ([]gen.StatsCamerasRow, error) {
	return []gen.StatsCamerasRow{{Label: "Nikon FM2", Rolls: 3}}, stub.err()
}

func (stub *queries) StatsLenses(_ context.Context, arg gen.StatsLensesParams) ([]gen.StatsLensesRow, error) {
	return []gen.StatsLensesRow{{Label: "50mm", Rolls: 2}}, stub.err()
}

func (stub *queries) StatsFilm(_ context.Context, _ uuid.UUID) ([]gen.StatsFilmRow, error) {
	return []gen.StatsFilmRow{{Label: "Kodak Gold", Rolls: 4}}, stub.err()
}

func (stub *queries) StatsFilmByBase(_ context.Context, _ uuid.UUID) ([]gen.StatsFilmByBaseRow, error) {
	return []gen.StatsFilmByBaseRow{{Label: "Kodak", Rolls: 6}}, stub.err()
}

func (stub *queries) StatsTimeline(_ context.Context, _ uuid.UUID) ([]gen.StatsTimelineRow, error) {
	return []gen.StatsTimelineRow{{Year: 2026, Month: 2, Rolls: 5}}, stub.err()
}

func (stub *queries) StatsFilmSpend(_ context.Context, _ uuid.UUID) ([]gen.StatsFilmSpendRow, error) {
	return []gen.StatsFilmSpendRow{{Year: 2026, Month: 2, Cost: 100, Missing: 1}}, stub.err()
}

func (stub *queries) StatsProcessingSpend(_ context.Context, _ uuid.UUID) ([]gen.StatsProcessingSpendRow, error) {
	return []gen.StatsProcessingSpendRow{{Year: 2026, Month: 2, Cost: 40}}, stub.err()
}

func newController(fail bool) *Controller {
	return New(statssvc.New(testutil.Store{Querier: &queries{fail: fail}}))
}

func TestSearchRollsReturnsRollsWithTheirScans(test *testing.T) {
	response, err := newController(false).SearchRolls(context.Background(), api.SearchRollsRequestObject{Params: api.SearchRollsParams{FocalLength: 50}})
	if err != nil {
		test.Fatal(err)
	}
	results := response.(api.SearchRolls200JSONResponse)
	if len(results) != 1 || len(results[0].Scans) != 1 || results[0].Scans[0].FrameNumber != 7 {
		test.Fatalf("results = %+v", results)
	}
}

func TestGearAndFilmStats(test *testing.T) {
	controller := newController(false)
	gear, err := controller.GetGearStats(context.Background(), api.GetGearStatsRequestObject{})
	if err != nil {
		test.Fatal(err)
	}
	if body := gear.(api.GetGearStats200JSONResponse); body.Cameras[0].Label != "Nikon FM2" || body.Lenses[0].Rolls != 2 {
		test.Fatalf("gear = %+v", body)
	}

	grouped := true
	for _, testCase := range []struct {
		name    string
		groupBy *bool
		want    string
	}{{"per stock", nil, "Kodak Gold"}, {"per base", &grouped, "Kodak"}} {
		film, err := controller.GetFilmStats(context.Background(), api.GetFilmStatsRequestObject{Params: api.GetFilmStatsParams{GroupByBase: testCase.groupBy}})
		if err != nil {
			test.Fatalf("%s: %v", testCase.name, err)
		}
		if items := film.(api.GetFilmStats200JSONResponse); items[0].Label != testCase.want {
			test.Fatalf("%s: items = %+v", testCase.name, items)
		}
	}
}

func TestTimelineAndSpendingStats(test *testing.T) {
	controller := newController(false)
	timeline, err := controller.GetTimelineStats(context.Background(), api.GetTimelineStatsRequestObject{})
	if err != nil {
		test.Fatal(err)
	}
	if years := timeline.(api.GetTimelineStats200JSONResponse); years[0].Year != 2026 || years[0].Months[1] != 5 {
		test.Fatalf("timeline = %+v", years)
	}

	spending, err := controller.GetSpendingStats(context.Background(), api.GetSpendingStatsRequestObject{})
	if err != nil {
		test.Fatal(err)
	}
	year := spending.(api.GetSpendingStats200JSONResponse)[0]
	if year.FilmCost != 100 || year.ProcessingCost != 40 || !year.Incomplete || year.Months[0].Month != 2 {
		test.Fatalf("spending = %+v", year)
	}
}

func TestStatsEndpointsPropagateServiceErrors(test *testing.T) {
	controller := newController(true)
	ctx := context.Background()
	checks := map[string]func() error{
		"search": func() error { _, err := controller.SearchRolls(ctx, api.SearchRollsRequestObject{}); return err },
		"gear":   func() error { _, err := controller.GetGearStats(ctx, api.GetGearStatsRequestObject{}); return err },
		"film":   func() error { _, err := controller.GetFilmStats(ctx, api.GetFilmStatsRequestObject{}); return err },
		"timeline": func() error {
			_, err := controller.GetTimelineStats(ctx, api.GetTimelineStatsRequestObject{})
			return err
		},
		"spending": func() error {
			_, err := controller.GetSpendingStats(ctx, api.GetSpendingStatsRequestObject{})
			return err
		},
	}
	for name, check := range checks {
		if err := check(); !errors.Is(err, errBoom) {
			test.Errorf("%s: err = %v", name, err)
		}
	}
}
