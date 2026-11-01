package scanner

import (
	"context"
	"meta-frames-server/internal/common/pointers"
	"testing"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/domain"
	scannersvc "meta-frames-server/internal/services/scanner"
	"meta-frames-server/internal/testutil"
)

func newController() (*Controller, context.Context) {
	ctx := requestctx.With(context.Background(), requestctx.Meta{OwnerID: uuid.New()})
	return New(scannersvc.New(testutil.Store{Querier: testutil.NewMemory()})), ctx
}

func TestScannerEndpoints(test *testing.T) {
	controller, ctx := newController()
	kind := api.ScannerKindFlatbed

	created, err := controller.CreateScanner(ctx, api.CreateScannerRequestObject{Body: &api.ScannerInput{Brand: "Epson", Model: pointers.To("V850"), Kind: &kind}})
	if err != nil {
		test.Fatal(err)
	}
	scanner := api.Scanner(created.(api.CreateScanner201JSONResponse))
	if scanner.Name != "Epson V850" || scanner.BuiltIn || scanner.Kind == nil || *scanner.Kind != kind {
		test.Fatalf("created = %+v", scanner)
	}

	updated, err := controller.PutScanner(ctx, api.PutScannerRequestObject{Id: scanner.Id, Body: &api.ScannerInput{Brand: "Epson", Model: pointers.To("V700")}})
	if mapped, ok := updated.(api.PutScanner200JSONResponse); err != nil || !ok || mapped.Name != "Epson V700" || mapped.Kind != nil {
		test.Fatalf("updated=%+v err=%v", updated, err)
	}
	putNew, err := controller.PutScanner(ctx, api.PutScannerRequestObject{Id: uuid.New(), Body: &api.ScannerInput{Brand: "Nikon", Model: pointers.To("Coolscan 9000")}})
	if _, ok := putNew.(api.PutScanner201JSONResponse); err != nil || !ok {
		test.Fatalf("put new=%+v err=%v", putNew, err)
	}
	_, err = controller.PutScanner(ctx, api.PutScannerRequestObject{Id: domain.ScannerNoritsuHS1800, Body: &api.ScannerInput{Brand: "Noritsu", Model: pointers.To("HS-1800")}})
	testutil.AssertAppError(test, err, apperror.KindConflict, "built_in_scanner")

	listed, err := controller.ListScanners(ctx, api.ListScannersRequestObject{})
	scanners := listed.(api.ListScanners200JSONResponse)
	if err != nil || len(scanners) != 4 || !scanners[0].BuiltIn || scanners[0].Name != "Frontier SP-3000" {
		test.Fatalf("scanners=%+v err=%v", scanners, err)
	}
	got, err := controller.GetScanner(ctx, api.GetScannerRequestObject{Id: scanner.Id})
	if mapped := got.(api.GetScanner200JSONResponse); err != nil || *mapped.Model != "V700" {
		test.Fatalf("got=%+v err=%v", got, err)
	}

	if _, err := controller.DeleteScanner(ctx, api.DeleteScannerRequestObject{Id: scanner.Id}); err != nil {
		test.Fatal(err)
	}
	_, err = controller.GetScanner(ctx, api.GetScannerRequestObject{Id: scanner.Id})
	testutil.AssertAppError(test, err, apperror.KindNotFound, "not_found")
	_, err = controller.CreateScanner(ctx, api.CreateScannerRequestObject{Body: &api.ScannerInput{Brand: " ", Model: pointers.To("x")}})
	testutil.AssertAppError(test, err, apperror.KindUnprocessable, "missing_fields")
}
