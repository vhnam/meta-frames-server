package roll

import (
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	rollsvc "meta-frames-server/internal/services/roll"
	"testing"

	"github.com/google/uuid"
)

func text(value string) *string { return &value }

func number(value int32) *int32 { return &value }

func TestToAPIRollSummary(test *testing.T) {
	summary := rollsvc.Summary{ListRollSummariesRow: gen.ListRollSummariesRow{
		ID: uuid.New(), Status: domain.RollStatusScanned, NegativesAtLab: true,
		CameraBrand: text("Nikon"), CameraModel: text("FM2"),
		ExpiryYear: number(2027), ExpiryMonth: number(6), ShotIso: number(800), Price: number(150000),
	}}
	mapped := ToAPIRollSummary(summary)

	if mapped.CameraName == nil || *mapped.CameraName != "Nikon FM2" {
		test.Fatalf("CameraName = %v", mapped.CameraName)
	}
	if !mapped.NegativesAtLab {
		test.Fatal("a scanned roll with negatives away shows the badge")
	}
	if mapped.Expiry == nil || mapped.Expiry.Year != 2027 || mapped.Expiry.Month == nil || *mapped.Expiry.Month != 6 {
		test.Fatalf("Expiry = %+v", mapped.Expiry)
	}
	if mapped.ShotIso == nil || *mapped.ShotIso != 800 || mapped.Price == nil || *mapped.Price != 150000 {
		test.Fatalf("shot ISO / price = %v / %v", mapped.ShotIso, mapped.Price)
	}
}

func TestToAPIRollSummaryOmitsUnknownValues(test *testing.T) {
	mapped := ToAPIRollSummary(rollsvc.Summary{ListRollSummariesRow: gen.ListRollSummariesRow{Status: domain.RollStatusInStock, ExpiryYear: number(2026)}})
	if mapped.CameraName != nil || mapped.ShotIso != nil || mapped.Price != nil {
		test.Fatalf("mapped = %+v", mapped)
	}
	if mapped.Expiry == nil || mapped.Expiry.Month != nil {
		test.Fatalf("an unknown expiry month must stay nil, got %+v", mapped.Expiry)
	}
	if ToAPIRollSummary(rollsvc.Summary{}).Expiry != nil {
		test.Fatal("no expiry year means no expiry")
	}
}

func TestToAPIRollDetailComputesTotalsAndKeepsListsNonNil(test *testing.T) {
	detail := rollsvc.Detail{
		Totals:   rollsvc.Totals{RollPrice: 150000, ProcessingPrice: 90000, Incomplete: true},
		Warnings: []string{"roll is expired"},
	}
	mapped := toAPIRollDetail(detail)

	if mapped.Totals.Total != 240000 || !mapped.Totals.Incomplete {
		test.Fatalf("totals = %+v", mapped.Totals)
	}
	if mapped.Processing == nil || mapped.Frames == nil || mapped.Lenses == nil {
		test.Fatal("processing, frames and lenses must serialize as [] rather than null")
	}
	if len(mapped.Warnings) != 1 {
		test.Fatalf("warnings = %v", mapped.Warnings)
	}
}
