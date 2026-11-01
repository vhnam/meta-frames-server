package scanner

import (
	"context"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	scannersvc "meta-frames-server/internal/services/scanner"
)

// Controller serves the scanner endpoints.
type Controller struct {
	scanners *scannersvc.Service
}

// New builds the controller around its service.
func New(scanners *scannersvc.Service) *Controller { return &Controller{scanners: scanners} }

func toAPIScanner(scanner gen.Scanner) api.Scanner {
	mapped := api.Scanner{
		Id: scanner.ID, Brand: scanner.Brand, Model: scanner.Model, Name: domain.ScannerName(scanner.Brand, scanner.Model),
		Notes: scanner.Notes, BuiltIn: scannersvc.IsBuiltIn(scanner), DefaultSettings: api.ScanSettings(domain.DecodeScanSettings(scanner.DefaultSettings)),
		CreatedAt: convert.Timestamp(scanner.CreatedAt), UpdatedAt: convert.Timestamp(scanner.UpdatedAt),
	}
	if scanner.Kind != nil {
		kind := api.ScannerKind(*scanner.Kind)
		mapped.Kind = &kind
	}
	return mapped
}

func fromAPIInput(body *api.ScannerInput) scannersvc.Input {
	input := scannersvc.Input{Brand: body.Brand, Model: body.Model, Notes: body.Notes}
	if body.DefaultSettings != nil {
		input.DefaultSettings = domain.ScanSettings(*body.DefaultSettings)
	}
	if body.Kind != nil {
		kind := string(*body.Kind)
		input.Kind = &kind
	}
	return input
}

func (controller *Controller) ListScanners(ctx context.Context, _ api.ListScannersRequestObject) (api.ListScannersResponseObject, error) {
	scanners, err := controller.scanners.List(ctx)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.Scanner, len(scanners))
	for index, scanner := range scanners {
		mapped[index] = toAPIScanner(scanner)
	}
	return api.ListScanners200JSONResponse(mapped), nil
}

func (controller *Controller) GetScanner(ctx context.Context, request api.GetScannerRequestObject) (api.GetScannerResponseObject, error) {
	scanner, err := controller.scanners.Get(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetScanner200JSONResponse(toAPIScanner(scanner)), nil
}

func (controller *Controller) CreateScanner(ctx context.Context, request api.CreateScannerRequestObject) (api.CreateScannerResponseObject, error) {
	scanner, err := controller.scanners.Create(ctx, uuid.New(), fromAPIInput(request.Body))
	if err != nil {
		return nil, err
	}
	return api.CreateScanner201JSONResponse(toAPIScanner(scanner)), nil
}

func (controller *Controller) PutScanner(ctx context.Context, request api.PutScannerRequestObject) (api.PutScannerResponseObject, error) {
	input := fromAPIInput(request.Body)
	scanner, err := controller.scanners.Update(ctx, request.Id, input)
	if apperror.IsNotFound(err) {
		if scanner, err = controller.scanners.Create(ctx, request.Id, input); err != nil {
			return nil, err
		}
		return api.PutScanner201JSONResponse(toAPIScanner(scanner)), nil
	}
	if err != nil {
		return nil, err
	}
	return api.PutScanner200JSONResponse(toAPIScanner(scanner)), nil
}

// kindLabels names each scanner kind for pickers; "" is a scanner whose kind is unknown.
var kindLabels = map[string]string{
	"": "Unknown kind", domain.ScannerKindMinilab: "Lab minilab", domain.ScannerKindVirtualDrum: "Virtual drum",
	domain.ScannerKindDrum: "Drum", domain.ScannerKindDedicatedFilm: "Film scanner", domain.ScannerKindFlatbed: "Flatbed",
	domain.ScannerKindCamera: "Camera scan",
}

func toAPIScanOptions(options []domain.ScanOption) []api.ScanOption {
	mapped := make([]api.ScanOption, len(options))
	for index, option := range options {
		mapped[index] = api.ScanOption{Key: option.Key, Label: option.Label, Type: api.ScanOptionType(option.Type), Default: option.Default}
		switch option.Type {
		case domain.ScanOptionEnum:
			values := make([]api.ScanOptionValue, len(option.Values))
			for valueIndex, value := range option.Values {
				values[valueIndex] = api.ScanOptionValue{Value: value.Value, Label: value.Label}
			}
			mapped[index].Values = &values
		case domain.ScanOptionInteger:
			mapped[index].Min, mapped[index].Max = pointers.To(option.Min), pointers.To(option.Max)
		}
	}
	return mapped
}

// ListScannerKinds lists every kind with its scan options, then the options of a scanner without a kind.
func (controller *Controller) ListScannerKinds(context.Context, api.ListScannerKindsRequestObject) (api.ListScannerKindsResponseObject, error) {
	kinds := make([]api.ScannerKindOptions, 0, len(domain.ScannerKinds)+1)
	for _, kind := range domain.ScannerKinds {
		apiKind := api.ScannerKind(kind)
		kinds = append(kinds, api.ScannerKindOptions{Kind: &apiKind, Label: kindLabels[kind], Options: toAPIScanOptions(domain.ScanOptionsFor(&kind))})
	}
	kinds = append(kinds, api.ScannerKindOptions{Label: kindLabels[""], Options: toAPIScanOptions(domain.ScanOptionsFor(nil))})
	return api.ListScannerKinds200JSONResponse(kinds), nil
}

func (controller *Controller) DeleteScanner(ctx context.Context, request api.DeleteScannerRequestObject) (api.DeleteScannerResponseObject, error) {
	if err := controller.scanners.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteScanner204Response{}, nil
}
