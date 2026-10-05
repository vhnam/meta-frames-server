package scan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/domain"
	scansvc "meta-frames-server/internal/services/scan"
)

// Controller serves the scan endpoints.
type Controller struct {
	scans *scansvc.Service
}

// New builds the controller around its service.
func New(scans *scansvc.Service) *Controller { return &Controller{scans: scans} }

func toAPIScan(view scansvc.View) api.Scan {
	scan := view.Scan
	return api.Scan{
		Id: scan.ID, ProcessingId: scan.ProcessingID, FrameId: scan.FrameID, FrameNumber: int(view.FrameNumber),
		Scanner: api.Scanner(scan.Scanner), FileName: scan.FileName, SizeBytes: scan.SizeBytes,
		FileUrl: "/scans/" + scan.ID.String() + "/file",
	}
}

func ToAPIScanRefs(refs []scansvc.RefView) []api.ScanRef {
	mapped := make([]api.ScanRef, len(refs))
	for index, ref := range refs {
		processingID := ref.ProcessingID
		mapped[index] = api.ScanRef{Id: ref.ID, FrameNumber: int(ref.FrameNumber), Scanner: api.Scanner(ref.Scanner), ProcessingId: &processingID}
	}
	return mapped
}

func ToAPIFrame(view scansvc.FrameView) api.Frame {
	return api.Frame{Id: view.Frame.ID, Number: int(view.Frame.Number), Notes: view.Frame.Notes, Scans: ToAPIScanRefs(view.Scans)}
}

func (controller *Controller) PutFrame(ctx context.Context, request api.PutFrameRequestObject) (api.PutFrameResponseObject, error) {
	view, err := controller.scans.SaveFrameNotes(ctx, request.Id, request.Number, request.Body.Notes)
	if err != nil {
		return nil, err
	}
	return api.PutFrame200JSONResponse(ToAPIFrame(view)), nil
}

func (controller *Controller) ListProcessingScans(ctx context.Context, request api.ListProcessingScansRequestObject) (api.ListProcessingScansResponseObject, error) {
	var scanner *string
	if request.Params.Scanner != nil {
		scanner = pointers.To(string(*request.Params.Scanner))
	}
	views, err := controller.scans.ListScans(ctx, request.Id, scanner)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.Scan, len(views))
	for index, view := range views {
		mapped[index] = toAPIScan(view)
	}
	return api.ListProcessingScans200JSONResponse(mapped), nil
}

func (controller *Controller) PreviewScanImport(ctx context.Context, request api.PreviewScanImportRequestObject) (api.PreviewScanImportResponseObject, error) {
	body := request.Body
	items, err := controller.scans.PreviewImport(ctx, request.Id, scansvc.ImportPreviewInput{
		FileNames: body.FileNames, StartFrame: body.StartFrame, Offset: body.Offset,
	})
	if err != nil {
		return nil, err
	}
	mapped := make([]api.ImportPreviewItem, len(items))
	for index, item := range items {
		mapped[index] = api.ImportPreviewItem{FileName: item.FileName, FrameNumber: item.FrameNumber}
		if item.Problem != "" {
			mapped[index].Error = pointers.To(item.Problem)
		}
	}
	return api.PreviewScanImport200JSONResponse(mapped), nil
}

func (controller *Controller) CompareFrame(ctx context.Context, request api.CompareFrameRequestObject) (api.CompareFrameResponseObject, error) {
	comparison, err := controller.scans.Compare(ctx, request.Id, request.Number)
	if err != nil {
		return nil, err
	}
	mapped := api.FrameComparison{
		FrameNumber: comparison.FrameNumber, Missing: make([]api.Scanner, len(comparison.Missing)),
		PreviousFrameNumber: comparison.PreviousFrameNumber, NextFrameNumber: comparison.NextFrameNumber,
	}
	for index, scanner := range comparison.Missing {
		mapped.Missing[index] = api.Scanner(scanner)
	}
	if comparison.Noritsu != nil {
		mapped.Noritsu = pointers.To(toAPIScan(*comparison.Noritsu))
	}
	if comparison.Frontier != nil {
		mapped.Frontier = pointers.To(toAPIScan(*comparison.Frontier))
	}
	return api.CompareFrame200JSONResponse(mapped), nil
}

// ---- import ----

// importForm collects the plain fields of the multipart import request.
type importForm struct {
	scanner         string
	replaceExisting bool
	pendingFrame    *int
}

// readFormField applies one non-file multipart field, returning an error for invalid values.
func (form *importForm) readFormField(name, value string) error {
	switch name {
	case "scanner":
		switch value {
		case domain.ScannerNoritsu, domain.ScannerFrontier, domain.ScannerOther:
			form.scanner = value
		default:
			return apperror.Unprocessable("invalid_scanner", "scanner must be noritsu, frontier or other")
		}
	case "onConflict":
		switch value {
		case "replace":
			form.replaceExisting = true
		case "skip", "":
		default:
			return apperror.Unprocessable("invalid_on_conflict", "onConflict must be skip or replace")
		}
	case "frameNumber":
		number, err := strconv.Atoi(value)
		if err != nil || number < 0 {
			return apperror.Unprocessable("invalid_frame_number", "frameNumber must be a non-negative integer")
		}
		form.pendingFrame = &number
	}
	return nil
}

// takeFrameNumber returns the frame number for the next file: the explicit field if one was sent,
// otherwise the number read from the file name.
func (form *importForm) takeFrameNumber(fileName string) (int, bool) {
	explicit := form.pendingFrame
	form.pendingFrame = nil
	if explicit != nil {
		return *explicit, true
	}
	return scansvc.FrameNumberFromName(fileName)
}

func readFieldValue(reader io.Reader) string {
	const maxFieldBytes = 256
	content, _ := io.ReadAll(io.LimitReader(reader, maxFieldBytes))
	return strings.TrimSpace(string(content))
}

// ImportScans attaches uploaded files to frames of the job's roll (UC-30).
// Multipart order matters: scanner (and onConflict) first, then an optional frameNumber field
// directly before each file part.
func (controller *Controller) ImportScans(ctx context.Context, request api.ImportScansRequestObject) (api.ImportScansResponseObject, error) {
	job, err := controller.scans.PrepareImport(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperror.Unprocessable("multipart_required", "multipart/form-data body required")
	}

	var (
		form   importForm
		result = api.ImportResult{Imported: []api.Scan{}, Skipped: []api.ImportFailure{}, Failed: []api.ImportFailure{}}
		files  int

		checkedScanner string
	)
	for {
		part, err := request.Body.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, apperror.Unprocessable("bad_multipart", err.Error())
		}
		if part.FormName() != "file" {
			if err := form.readFormField(part.FormName(), readFieldValue(part)); err != nil {
				return nil, err
			}
			continue
		}

		if form.scanner == "" {
			return nil, apperror.Unprocessable("scanner_first", "the scanner field must come before the files")
		}
		if checkedScanner != form.scanner {
			if err := controller.scans.CheckScannerOrdered(ctx, job.ID, form.scanner); err != nil {
				return nil, err
			}
			checkedScanner = form.scanner
		}
		files++
		fileName := part.FileName()
		frameNumber, found := form.takeFrameNumber(fileName)
		if !found {
			result.Failed = append(result.Failed, api.ImportFailure{FileName: fileName, Reason: "no frame number; send a frameNumber field"})
			continue
		}
		outcome, err := controller.scans.ImportFile(ctx, job, scansvc.ImportOptions{Scanner: form.scanner, ReplaceExisting: form.replaceExisting}, frameNumber, fileName, part)
		var rejected *scansvc.FileRejectedError
		switch {
		case errors.As(err, &rejected):
			result.Failed = append(result.Failed, api.ImportFailure{FileName: fileName, Reason: rejected.Reason})
		case err != nil:
			return nil, err
		case outcome.Skipped:
			result.Skipped = append(result.Skipped, api.ImportFailure{FileName: fileName, Reason: "a scan already exists for this frame and scanner"})
		default:
			result.Imported = append(result.Imported, toAPIScan(*outcome.Scan))
		}
	}
	if form.scanner == "" {
		return nil, apperror.Unprocessable("invalid_scanner", "scanner is required")
	}
	if files == 0 {
		return nil, apperror.Unprocessable("no_files", "no files uploaded")
	}

	processing, err := controller.scans.CompleteImport(ctx, request.Id, len(result.Imported))
	if err != nil {
		return nil, err
	}
	if processing.Job.ScansReceivedAt != nil {
		result.ScansReceivedAt = convert.APIDate(*processing.Job.ScansReceivedAt)
	}
	return api.ImportScans200JSONResponse(result), nil
}

// ---- file download ----

type scanFileResponse struct{ file scansvc.ScanFile }

func (response scanFileResponse) VisitGetScanFileResponse(writer http.ResponseWriter) error {
	defer response.file.Content.Close()
	writer.Header().Set("Content-Type", response.file.ContentType)
	writer.Header().Set("Content-Length", strconv.FormatInt(response.file.Size, 10))
	writer.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", response.file.FileName))
	writer.Header().Set("Cache-Control", "private, max-age=3600")
	_, err := io.Copy(writer, response.file.Content)
	return err
}

func (controller *Controller) GetScanFile(ctx context.Context, request api.GetScanFileRequestObject) (api.GetScanFileResponseObject, error) {
	file, err := controller.scans.OpenFile(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return scanFileResponse{file: file}, nil
}

func (controller *Controller) GetScan(ctx context.Context, request api.GetScanRequestObject) (api.GetScanResponseObject, error) {
	view, err := controller.scans.Get(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetScan200JSONResponse(toAPIScan(view)), nil
}

func (controller *Controller) DeleteScan(ctx context.Context, request api.DeleteScanRequestObject) (api.DeleteScanResponseObject, error) {
	if err := controller.scans.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteScan204Response{}, nil
}

func (controller *Controller) ListRollFrames(ctx context.Context, request api.ListRollFramesRequestObject) (api.ListRollFramesResponseObject, error) {
	views, err := controller.scans.ListFrames(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.Frame, len(views))
	for index, view := range views {
		mapped[index] = ToAPIFrame(view)
	}
	return api.ListRollFrames200JSONResponse(mapped), nil
}

func (controller *Controller) GetFrame(ctx context.Context, request api.GetFrameRequestObject) (api.GetFrameResponseObject, error) {
	view, err := controller.scans.GetFrame(ctx, request.Id, request.Number)
	if err != nil {
		return nil, err
	}
	return api.GetFrame200JSONResponse(ToAPIFrame(view)), nil
}
