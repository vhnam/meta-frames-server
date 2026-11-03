package lab

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/testutil"
)

func newMemoryLabService() (*Service, *testutil.Memory, context.Context) {
	memory := testutil.NewMemory()
	ctx := requestctx.With(context.Background(), requestctx.Meta{OwnerID: uuid.New()})
	return New(testutil.Store{Querier: memory}), memory, ctx
}

func TestLabKeepsItsScannersInOrder(test *testing.T) {
	service, memory, ctx := newMemoryLabService()
	own := uuid.New()
	memory.Scanners[own] = gen.Scanner{ID: own, OwnerID: requestctx.Owner(ctx), Brand: "Noritsu"}

	// one given twice keeps its first place
	scanners := []uuid.UUID{own, domain.ScannerFrontierSP3000, own}
	lab, err := service.Create(ctx, uuid.New(), Input{Name: "Lab A", ScannerIDs: &scanners})
	if err != nil || !slices.Equal(lab.ScannerIDs, []uuid.UUID{own, domain.ScannerFrontierSP3000}) {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}

	reordered := []uuid.UUID{domain.ScannerFrontierSP3000, domain.ScannerNoritsuHS1800}
	lab, err = service.Update(ctx, lab.Lab.ID, Input{Name: "Lab A", ScannerIDs: &reordered})
	if err != nil || !slices.Equal(lab.ScannerIDs, reordered) {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}

	// without scannerIds the set stays; an empty list clears it
	lab, err = service.Update(ctx, lab.Lab.ID, Input{Name: "Lab B"})
	if err != nil || !slices.Equal(lab.ScannerIDs, reordered) {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}
	lab, err = service.Update(ctx, lab.Lab.ID, Input{Name: "Lab B", ScannerIDs: &[]uuid.UUID{}})
	if err != nil || lab.ScannerIDs == nil || len(lab.ScannerIDs) != 0 {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}

	labs, err := service.List(ctx)
	if err != nil || len(labs) != 1 || len(labs[0].ScannerIDs) != 0 {
		test.Fatalf("labs=%+v err=%v", labs, err)
	}
}

func TestLabDefaultScannerIsOptional(test *testing.T) {
	service, _, ctx := newMemoryLabService()
	scanners := []uuid.UUID{domain.ScannerNoritsuHS1800, domain.ScannerFrontierSP3000}

	lab, err := service.Create(ctx, uuid.New(), Input{Name: "Lab A", ScannerIDs: &scanners})
	if err != nil || lab.DefaultScannerID != nil {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}

	frontier := domain.ScannerFrontierSP3000
	lab, err = service.Update(ctx, lab.Lab.ID, Input{Name: "Lab A", ScannerIDs: &scanners, DefaultScannerID: &frontier})
	if err != nil || lab.DefaultScannerID == nil || *lab.DefaultScannerID != frontier {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}

	// without scannerIds the default stays; with them and no default it clears
	lab, err = service.Update(ctx, lab.Lab.ID, Input{Name: "Lab B"})
	if err != nil || lab.DefaultScannerID == nil || *lab.DefaultScannerID != frontier {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}
	lab, err = service.Update(ctx, lab.Lab.ID, Input{Name: "Lab B", ScannerIDs: &scanners})
	if err != nil || lab.DefaultScannerID != nil {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}

	// the default must be one of the lab's scanners
	stranger := uuid.New()
	_, err = service.Update(ctx, lab.Lab.ID, Input{Name: "Lab B", ScannerIDs: &scanners, DefaultScannerID: &stranger})
	assertAppError(test, err, apperror.KindUnprocessable, "unknown_default_scanner")
	_, err = service.Update(ctx, lab.Lab.ID, Input{Name: "Lab B", DefaultScannerID: &frontier})
	assertAppError(test, err, apperror.KindUnprocessable, "unknown_default_scanner")
}

func TestLabRefusesScannersItCannotSee(test *testing.T) {
	service, memory, ctx := newMemoryLabService()
	stranger := uuid.New()
	memory.Scanners[stranger] = gen.Scanner{ID: stranger, OwnerID: uuid.New(), Brand: "Imacon"}

	for _, scannerID := range []uuid.UUID{stranger, uuid.New()} {
		scanners := []uuid.UUID{scannerID}
		_, err := service.Create(ctx, uuid.New(), Input{Name: "Lab", ScannerIDs: &scanners})
		assertAppError(test, err, apperror.KindUnprocessable, "unknown_scanner")
	}
}
