package scanner

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/testutil"
)

func newScannerService() (*Service, *testutil.Memory, context.Context) {
	memory := testutil.NewMemory()
	ctx := requestctx.With(context.Background(), requestctx.Meta{OwnerID: uuid.New()})
	return New(testutil.Store{Querier: memory}), memory, ctx
}

func TestScannerListShowsBuiltInFirstAndHidesOtherAccounts(test *testing.T) {
	service, memory, ctx := newScannerService()
	if _, err := service.Create(ctx, uuid.New(), Input{Brand: "Epson", Model: pointers.To("V850")}); err != nil {
		test.Fatal(err)
	}
	stranger := uuid.New()
	memory.Scanners[stranger] = gen.Scanner{ID: stranger, OwnerID: uuid.New(), Brand: "Imacon", Model: pointers.To("Flextight X5")}

	scanners, err := service.List(ctx)
	if err != nil || len(scanners) != 3 {
		test.Fatalf("scanners=%+v err=%v", scanners, err)
	}
	if !IsBuiltIn(scanners[0]) || !IsBuiltIn(scanners[1]) || IsBuiltIn(scanners[2]) || scanners[2].Brand != "Epson" {
		test.Fatalf("order = %+v", scanners)
	}
	assertAppError(test, func() error { _, err := service.Get(ctx, stranger); return err }(), apperror.KindNotFound, "not_found")
	if scanner, err := service.Get(ctx, domain.ScannerNoritsuHS1800); err != nil || *scanner.Model != "HS-1800" {
		test.Fatalf("built-in=%+v err=%v", scanner, err)
	}
}

func TestScannerCreateValidatesAndTrims(test *testing.T) {
	service, _, ctx := newScannerService()
	scanner, err := service.Create(ctx, uuid.New(), Input{
		Brand: " Sony ", Model: pointers.To(" A7R IV + Laowa 100mm "), Kind: pointers.To(domain.ScannerKindCamera), Notes: pointers.To("  "),
	})
	if err != nil || scanner.Brand != "Sony" || *scanner.Model != "A7R IV + Laowa 100mm" || scanner.Notes != nil || IsBuiltIn(scanner) {
		test.Fatalf("scanner=%+v err=%v", scanner, err)
	}
	_, err = service.Create(ctx, uuid.New(), Input{Brand: "  ", Model: pointers.To("A1")})
	assertAppError(test, err, apperror.KindUnprocessable, "missing_fields")
	_, err = service.Create(ctx, uuid.New(), Input{Brand: "Sony", Model: pointers.To("A1"), Kind: pointers.To("phone")})
	assertAppError(test, err, apperror.KindUnprocessable, "invalid_scanner_kind")
}

func TestScannerModelIsOptional(test *testing.T) {
	service, _, ctx := newScannerService()
	// a lab's Noritsu whose model nobody knows: a blank model is no model
	noritsu, err := service.Create(ctx, uuid.New(), Input{Brand: "Noritsu", Model: pointers.To("  ")})
	if err != nil || noritsu.Model != nil || domain.ScannerName(noritsu.Brand, noritsu.Model) != "Noritsu" {
		test.Fatalf("scanner=%+v err=%v", noritsu, err)
	}
	// it does not clash with the built-in Noritsu HS-1800, but a second one without a model does
	_, err = service.Create(ctx, uuid.New(), Input{Brand: "noritsu"})
	assertAppError(test, err, apperror.KindConflict, "already_exists")
}

func TestScannerNamesAreUnique(test *testing.T) {
	service, _, ctx := newScannerService()
	_, err := service.Create(ctx, uuid.New(), Input{Brand: "noritsu", Model: pointers.To("hs-1800")})
	assertAppError(test, err, apperror.KindConflict, "already_exists")

	epson, err := service.Create(ctx, uuid.New(), Input{Brand: "Epson", Model: pointers.To("V850")})
	if err != nil {
		test.Fatal(err)
	}
	_, err = service.Create(ctx, uuid.New(), Input{Brand: "EPSON", Model: pointers.To("v850")})
	assertAppError(test, err, apperror.KindConflict, "already_exists")
	_, err = service.Create(ctx, epson.ID, Input{Brand: "Epson", Model: pointers.To("V700")})
	assertAppError(test, err, apperror.KindConflict, "already_exists")
	if _, err := service.Update(ctx, epson.ID, Input{Brand: "Epson", Model: pointers.To("V850"), Notes: pointers.To("with Betterscanning holders")}); err != nil {
		test.Fatalf("keeping its own name must work, got %v", err)
	}
}

func TestScannerBuiltInIsReadOnly(test *testing.T) {
	service, _, ctx := newScannerService()
	_, err := service.Update(ctx, domain.ScannerFrontierSP3000, Input{Brand: "Frontier", Model: pointers.To("SP-500")})
	assertAppError(test, err, apperror.KindConflict, "built_in_scanner")
	assertAppError(test, service.Delete(ctx, domain.ScannerFrontierSP3000), apperror.KindConflict, "built_in_scanner")
	_, err = service.Update(ctx, uuid.New(), Input{Brand: "Frontier", Model: pointers.To("SP-500")})
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestScannerDeleteRefusesAScannerInUse(test *testing.T) {
	service, memory, ctx := newScannerService()
	scanner, err := service.Create(ctx, uuid.New(), Input{Brand: "Epson", Model: pointers.To("V850")})
	if err != nil {
		test.Fatal(err)
	}
	memory.ScanOrders = append(memory.ScanOrders, gen.ProcessingScanOrder{ProcessingID: uuid.New(), ScannerID: scanner.ID})
	assertAppError(test, service.Delete(ctx, scanner.ID), apperror.KindConflict, "scanner_in_use")

	memory.ScanOrders = nil
	if err := service.Delete(ctx, scanner.ID); err != nil {
		test.Fatal(err)
	}
	assertAppError(test, service.Delete(ctx, scanner.ID), apperror.KindNotFound, "not_found")
	if _, err := service.Create(ctx, uuid.New(), Input{Brand: "Epson", Model: pointers.To("V850")}); err != nil {
		test.Fatalf("a deleted scanner frees its name, got %v", err)
	}
}

func TestScannerPick(test *testing.T) {
	service, _, ctx := newScannerService()
	scannerID := uuid.New()

	if picked, err := service.Pick(ctx, &scannerID, nil); err != nil || picked != scannerID {
		test.Fatalf("picked=%v err=%v", picked, err)
	}
	for code, want := range map[string]uuid.UUID{"noritsu": domain.ScannerNoritsuHS1800, "frontier": domain.ScannerFrontierSP3000} {
		if picked, err := service.Pick(ctx, nil, &code); err != nil || picked != want {
			test.Fatalf("%s: picked=%v err=%v", code, picked, err)
		}
	}
	code := "epson"
	_, err := service.Pick(ctx, nil, &code)
	assertAppError(test, err, apperror.KindUnprocessable, "invalid_scanner")
	_, err = service.Pick(ctx, nil, nil)
	assertAppError(test, err, apperror.KindUnprocessable, "invalid_scanner")
	noritsu := domain.ScannerCodeNoritsu
	_, err = service.Pick(ctx, &scannerID, &noritsu)
	assertAppError(test, err, apperror.KindUnprocessable, "invalid_scanner")
}

func TestScannerOtherIsTheAccountsUnknownScanner(test *testing.T) {
	service, _, ctx := newScannerService()
	other := domain.ScannerCodeOther

	_, err := service.Pick(ctx, nil, &other)
	assertAppError(test, err, apperror.KindUnprocessable, "unknown_scanner")

	created, err := service.PickForOrder(ctx, nil, &other)
	if err != nil {
		test.Fatal(err)
	}
	unknown, err := service.Get(ctx, created)
	if err != nil || domain.ScannerName(unknown.Brand, unknown.Model) != "Unknown scanner" || IsBuiltIn(unknown) {
		test.Fatalf("unknown=%+v err=%v", unknown, err)
	}
	if again, err := service.PickForOrder(ctx, nil, &other); err != nil || again != created {
		test.Fatalf("again=%v err=%v", again, err)
	}
	if found, err := service.Pick(ctx, nil, &other); err != nil || found != created {
		test.Fatalf("found=%v err=%v", found, err)
	}
}

func assertAppError(test *testing.T, err error, kind apperror.Kind, code string) {
	test.Helper()
	testutil.AssertAppError(test, err, kind, code)
}

func TestScannerDefaultSettingsFitItsKind(test *testing.T) {
	service, _, ctx := newScannerService()
	camera := domain.ScannerKindCamera
	rig, err := service.Create(ctx, uuid.New(), Input{Brand: "Sony", Model: pointers.To("A7R IV"), Kind: &camera, DefaultSettings: domain.ScanSettings{"pixel_shift": true}})
	if err != nil || domain.DecodeScanSettings(rig.DefaultSettings)["pixel_shift"] != true {
		test.Fatalf("rig=%+v err=%v", rig, err)
	}
	_, err = service.Update(ctx, rig.ID, Input{Brand: "Sony", Model: pointers.To("A7R IV"), DefaultSettings: domain.ScanSettings{"pixel_shift": true}})
	assertAppError(test, err, apperror.KindUnprocessable, "invalid_scan_settings")
}
