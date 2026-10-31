package processing

import (
	"meta-frames-server/internal/api"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	processingsvc "meta-frames-server/internal/services/processing"
	"testing"
	"time"

	"github.com/google/uuid"
)

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func number(value int32) *int32 { return &value }

func TestToAPIProcessing(test *testing.T) {
	labID := uuid.New()
	received := date(2026, time.October, 1)
	view := processingsvc.View{
		Job:     gen.Processing{ID: uuid.New(), LabID: &labID, Type: domain.JobTypeDevelopScan, Process: "C-41", SentAt: date(2026, time.September, 1), ScansReceivedAt: &received, Price: number(90000)},
		LabName: "Lab A",
		ScanOrders: []processingsvc.ScanOrder{
			{ScannerID: domain.ScannerNoritsuHS1800, ScannerName: "Noritsu HS-1800", Settings: domain.ScanSettings{"resolution": domain.ResolutionHiRes}, ScanCount: 3},
			{ScannerID: uuid.New(), ScannerName: "Epson V850"},
		},
	}
	mapped := ToAPIProcessing(view)

	if mapped.LabName == nil || *mapped.LabName != "Lab A" || mapped.ScansReceivedAt == nil || mapped.NegativesReturnedAt != nil {
		test.Fatalf("mapped = %+v", mapped)
	}
	first, second := mapped.ScanOrders[0], mapped.ScanOrders[1]
	if first.Scanner != api.Noritsu || first.ScannerName != "Noritsu HS-1800" || !first.HiRes || first.Settings["resolution"] != "hi_res" || first.ScanCount != 3 {
		test.Fatalf("scan orders = %v", mapped.ScanOrders)
	}
	if second.Scanner != api.Other || second.ScannerName != "Epson V850" || second.ScannerId == domain.ScannerNoritsuHS1800 {
		test.Fatalf("scan orders = %v", mapped.ScanOrders)
	}
	if home := ToAPIProcessing(processingsvc.View{}); home.LabName != nil || home.ScanOrders == nil {
		test.Fatalf("home job = %+v", home)
	}
}
