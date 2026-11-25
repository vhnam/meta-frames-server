package roll

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
)

func TestStoredBulkReplayPreservesSummaryFields(test *testing.T) {
	started := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	finished := started.Add(24 * time.Hour)
	stamp := pgtype.Timestamptz{Time: started.Add(time.Microsecond), Valid: true}
	populated := Summary{
		ListRollSummariesRow: gen.ListRollSummariesRow{
			ID: uuid.New(), FilmStockID: uuid.New(), CameraID: pointers.To(uuid.New()), OwnerID: uuid.New(),
			Format: 135, Exposures: 36, Status: "done_shooting", ShotIso: pointers.To(int32(800)),
			ExpiryYear: pointers.To(int32(2027)), ExpiryMonth: pointers.To(int32(5)), Price: pointers.To(int32(0)),
			Description: pointers.To("One roll"), StartedAt: &started, FinishedAt: &finished,
			CreatedAt: stamp, UpdatedAt: stamp, DeletedAt: stamp, ScanRequestedAt: stamp, Version: 1<<53 + 1,
			Target: pointers.To("scanned"), DevelopBy: pointers.To("lab"), ScanBy: pointers.To("home"),
			StockBrand: "Kodak", StockName: "Gold", BoxIso: "200",
			CameraBrand: pointers.To("Leica"), CameraModel: pointers.To("M3"),
			NegativesAtLab: true, OpenHandoffID: uuid.New(), Developed: true, Scanned: true,
			OpenJobTypes: []string{"develop", "scan"}, OpenAtLab: true, StageJobs: []byte(`[{"type":"scan"}]`),
		},
		Stage: "processing", StageDetail: pointers.To("at_lab"),
	}
	want := []Summary{populated, {ListRollSummariesRow: gen.ListRollSummariesRow{ID: uuid.New()}}}
	raw, err := json.Marshal(storedBulk{Summaries: want})
	if err != nil {
		test.Fatal(err)
	}
	// An envelope must replay without querying current rows.
	got, err := replayStored(context.Background(), nil, gen.IdempotencyKey{Response: raw})
	if err != nil {
		test.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		test.Fatalf("replayed summaries differ:\n got: %+v\nwant: %+v", got, want)
	}
}
