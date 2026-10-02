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
		Job:      gen.Processing{ID: uuid.New(), LabID: &labID, Type: domain.JobTypeDevelopScan, Process: "C-41", SentAt: date(2026, time.September, 1), ScansReceivedAt: &received, Price: number(90000)},
		LabName:  "Lab A",
		Scanners: []string{domain.ScannerNoritsu, domain.ScannerFrontier},
	}
	mapped := ToAPIProcessing(view)

	if mapped.LabName == nil || *mapped.LabName != "Lab A" || mapped.ScansReceivedAt == nil || mapped.NegativesReturnedAt != nil {
		test.Fatalf("mapped = %+v", mapped)
	}
	if len(mapped.Scanners) != 2 || mapped.Scanners[1] != api.Frontier {
		test.Fatalf("scanners = %v", mapped.Scanners)
	}
	if home := ToAPIProcessing(processingsvc.View{}); home.LabName != nil || home.Scanners == nil {
		test.Fatalf("home job = %+v", home)
	}
}
