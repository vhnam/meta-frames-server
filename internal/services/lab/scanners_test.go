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

	// the first scanner is the lab's default; one given twice keeps its first place
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
