// Package testutil holds in-memory fakes shared by unit tests of the layers above the database.
package testutil

import (
	"context"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/db/gen"
)

// Memory is a tiny in-memory stand-in for the generated queries. It embeds the Querier
// interface, so calling a method it does not implement panics and flags an unexpected query.
type Memory struct {
	gen.Querier

	Cameras       map[uuid.UUID]gen.Camera
	Lenses        map[uuid.UUID]gen.Lens
	Links         []gen.CameraLens
	LoadedCameras map[uuid.UUID]bool
	Stocks        map[uuid.UUID]gen.FilmStock
	Rolls         map[uuid.UUID]gen.Roll
	Jobs          map[uuid.UUID]gen.Processing
	Frames        map[uuid.UUID]gen.Frame
	Scans         map[uuid.UUID]gen.Scan

	RollLenses  []gen.RollLens
	Labs        map[uuid.UUID]gen.Lab
	AtLabRows   []gen.NegativesAtLabRow
	Inventory   []gen.InventoryRowsRow
	Expiries    []gen.SoonestExpiriesRow
	Idempotency map[string]gen.IdempotencyKey
	Spend       gen.RollSpendRow

	HasOpenJob       bool
	HasReceivedScans bool
	InsertedLensIDs  []uuid.UUID
}

func NewMemory() *Memory {
	return &Memory{
		Cameras: map[uuid.UUID]gen.Camera{}, Lenses: map[uuid.UUID]gen.Lens{}, LoadedCameras: map[uuid.UUID]bool{},
		Stocks: map[uuid.UUID]gen.FilmStock{}, Rolls: map[uuid.UUID]gen.Roll{}, Jobs: map[uuid.UUID]gen.Processing{},
		Frames: map[uuid.UUID]gen.Frame{}, Scans: map[uuid.UUID]gen.Scan{}, Idempotency: map[string]gen.IdempotencyKey{}, Labs: map[uuid.UUID]gen.Lab{},
	}
}

// ---- cameras ----

func (queries *Memory) GetCamera(_ context.Context, id uuid.UUID) (gen.Camera, error) {
	camera, found := queries.Cameras[id]
	if !found || camera.DeletedAt.Valid {
		return gen.Camera{}, pgx.ErrNoRows
	}
	return camera, nil
}

func (queries *Memory) GetCameraForUpdate(ctx context.Context, id uuid.UUID) (gen.Camera, error) {
	return queries.GetCamera(ctx, id)
}

func (queries *Memory) InsertCamera(_ context.Context, arg gen.InsertCameraParams) (gen.Camera, error) {
	camera := gen.Camera{ID: arg.ID, Brand: arg.Brand, Model: arg.Model, Mount: arg.Mount, Description: arg.Description, HasFixedLens: arg.HasFixedLens, IsActive: true}
	queries.Cameras[arg.ID] = camera
	return camera, nil
}

func (queries *Memory) UpdateCamera(_ context.Context, arg gen.UpdateCameraParams) (gen.Camera, error) {
	camera := queries.Cameras[arg.ID]
	camera.Brand, camera.Model, camera.Mount, camera.Description = arg.Brand, arg.Model, arg.Mount, arg.Description
	queries.Cameras[arg.ID] = camera
	return camera, nil
}

func (queries *Memory) SetCameraActive(_ context.Context, arg gen.SetCameraActiveParams) (gen.Camera, error) {
	camera := queries.Cameras[arg.ID]
	camera.IsActive = arg.IsActive
	queries.Cameras[arg.ID] = camera
	return camera, nil
}

func (queries *Memory) CameraIsLoaded(_ context.Context, cameraID *uuid.UUID) (bool, error) {
	return queries.LoadedCameras[*cameraID], nil
}

func (queries *Memory) SetBuiltInLensActive(_ context.Context, arg gen.SetBuiltInLensActiveParams) error {
	for _, link := range queries.Links {
		if lens := queries.Lenses[link.LensID]; link.CameraID == arg.CameraID && lens.IsBuiltIn {
			lens.IsActive = arg.IsActive
			queries.Lenses[lens.ID] = lens
		}
	}
	return nil
}

func (queries *Memory) ListBuiltInLensLinks(context.Context) ([]gen.CameraLens, error) {
	var links []gen.CameraLens
	for _, link := range queries.Links {
		if lens := queries.Lenses[link.LensID]; lens.IsBuiltIn && !lens.DeletedAt.Valid {
			links = append(links, link)
		}
	}
	return links, nil
}

func (queries *Memory) ListLoadedRolls(context.Context) ([]gen.ListLoadedRollsRow, error) {
	return nil, nil
}

// ---- lenses ----

func (queries *Memory) GetLens(_ context.Context, id uuid.UUID) (gen.Lens, error) {
	lens, found := queries.Lenses[id]
	if !found || lens.DeletedAt.Valid {
		return gen.Lens{}, pgx.ErrNoRows
	}
	return lens, nil
}

func (queries *Memory) GetLensForUpdate(ctx context.Context, id uuid.UUID) (gen.Lens, error) {
	return queries.GetLens(ctx, id)
}

func (queries *Memory) InsertLens(_ context.Context, arg gen.InsertLensParams) (gen.Lens, error) {
	lens := gen.Lens{
		ID: arg.ID, Brand: arg.Brand, Model: arg.Model, Mount: arg.Mount, Description: arg.Description,
		FocalLength: arg.FocalLength, MaxAperture: arg.MaxAperture, IsBuiltIn: arg.IsBuiltIn, IsActive: true,
	}
	queries.Lenses[arg.ID] = lens
	queries.InsertedLensIDs = append(queries.InsertedLensIDs, arg.ID)
	return lens, nil
}

func (queries *Memory) UpdateLens(_ context.Context, arg gen.UpdateLensParams) (gen.Lens, error) {
	lens := queries.Lenses[arg.ID]
	lens.Brand, lens.Model, lens.Mount, lens.Description = arg.Brand, arg.Model, arg.Mount, arg.Description
	lens.FocalLength, lens.MaxAperture = arg.FocalLength, arg.MaxAperture
	queries.Lenses[arg.ID] = lens
	return lens, nil
}

func (queries *Memory) SetLensActive(_ context.Context, arg gen.SetLensActiveParams) (gen.Lens, error) {
	lens := queries.Lenses[arg.ID]
	lens.IsActive = arg.IsActive
	queries.Lenses[arg.ID] = lens
	return lens, nil
}

func (queries *Memory) InsertCameraLens(_ context.Context, arg gen.InsertCameraLensParams) error {
	queries.Links = append(queries.Links, gen.CameraLens{CameraID: arg.CameraID, LensID: arg.LensID})
	return nil
}

func (queries *Memory) ListCameraLenses(_ context.Context, cameraID uuid.UUID) ([]gen.Lens, error) {
	var lenses []gen.Lens
	for _, link := range queries.Links {
		if lens := queries.Lenses[link.LensID]; link.CameraID == cameraID && !lens.DeletedAt.Valid {
			lenses = append(lenses, lens)
		}
	}
	return lenses, nil
}

func (queries *Memory) GetLensesByIDs(_ context.Context, ids []uuid.UUID) ([]gen.Lens, error) {
	var lenses []gen.Lens
	for _, id := range ids {
		if lens, found := queries.Lenses[id]; found && !lens.DeletedAt.Valid {
			lenses = append(lenses, lens)
		}
	}
	return lenses, nil
}

// ---- film stocks ----

func (queries *Memory) GetFilmStock(_ context.Context, id uuid.UUID) (gen.FilmStock, error) {
	stock, found := queries.Stocks[id]
	if !found || stock.DeletedAt.Valid {
		return gen.FilmStock{}, pgx.ErrNoRows
	}
	return stock, nil
}

func (queries *Memory) GetFilmStockForUpdate(ctx context.Context, id uuid.UUID) (gen.FilmStock, error) {
	return queries.GetFilmStock(ctx, id)
}

func (queries *Memory) InsertFilmStock(_ context.Context, arg gen.InsertFilmStockParams) (gen.FilmStock, error) {
	stock := gen.FilmStock{
		ID: arg.ID, Brand: arg.Brand, Name: arg.Name, Type: arg.Type, BoxIso: arg.BoxIso, Process: arg.Process,
		Packaging: arg.Packaging, StockOrigin: arg.StockOrigin, PackOrigin: arg.PackOrigin, Description: arg.Description,
		BaseStockID: arg.BaseStockID,
	}
	queries.Stocks[arg.ID] = stock
	return stock, nil
}

func (queries *Memory) UpdateFilmStock(_ context.Context, arg gen.UpdateFilmStockParams) (gen.FilmStock, error) {
	stock := queries.Stocks[arg.ID]
	stock.Brand, stock.Name, stock.BaseStockID = arg.Brand, arg.Name, arg.BaseStockID
	queries.Stocks[arg.ID] = stock
	return stock, nil
}

func (queries *Memory) ListSiblingStocks(_ context.Context, arg gen.ListSiblingStocksParams) ([]gen.FilmStock, error) {
	var siblings []gen.FilmStock
	for _, stock := range queries.Stocks {
		if !stock.DeletedAt.Valid && stock.BaseStockID != nil && arg.BaseStockID != nil && *stock.BaseStockID == *arg.BaseStockID && stock.ID != arg.ID {
			siblings = append(siblings, stock)
		}
	}
	return siblings, nil
}

func (queries *Memory) ListDerivedStocks(_ context.Context, baseID *uuid.UUID) ([]gen.FilmStock, error) {
	var derived []gen.FilmStock
	for _, stock := range queries.Stocks {
		if !stock.DeletedAt.Valid && stock.BaseStockID != nil && *stock.BaseStockID == *baseID {
			derived = append(derived, stock)
		}
	}
	sort.Slice(derived, func(left, right int) bool { return derived[left].Name < derived[right].Name })
	return derived, nil
}

// ---- rolls, jobs, frames, scans ----

func (queries *Memory) GetRoll(_ context.Context, id uuid.UUID) (gen.Roll, error) {
	roll, found := queries.Rolls[id]
	if !found || roll.DeletedAt.Valid {
		return gen.Roll{}, pgx.ErrNoRows
	}
	return roll, nil
}

func (queries *Memory) GetRollForUpdate(ctx context.Context, id uuid.UUID) (gen.Roll, error) {
	return queries.GetRoll(ctx, id)
}

func (queries *Memory) SetRollStatus(_ context.Context, arg gen.SetRollStatusParams) error {
	roll := queries.Rolls[arg.ID]
	roll.Status = arg.Status
	queries.Rolls[arg.ID] = roll
	return nil
}

func (queries *Memory) RollHasOpenJob(context.Context, uuid.UUID) (bool, error) {
	return queries.HasOpenJob, nil
}

func (queries *Memory) RollHasScansReceived(context.Context, uuid.UUID) (bool, error) {
	return queries.HasReceivedScans, nil
}

func (queries *Memory) GetProcessing(_ context.Context, id uuid.UUID) (gen.Processing, error) {
	job, found := queries.Jobs[id]
	if !found || job.DeletedAt.Valid {
		return gen.Processing{}, pgx.ErrNoRows
	}
	return job, nil
}

func (queries *Memory) GetProcessingForUpdate(ctx context.Context, id uuid.UUID) (gen.Processing, error) {
	return queries.GetProcessing(ctx, id)
}

func (queries *Memory) SetScansReceived(_ context.Context, arg gen.SetScansReceivedParams) (gen.Processing, error) {
	job := queries.Jobs[arg.ID]
	job.ScansReceivedAt = arg.ScansReceivedAt
	queries.Jobs[arg.ID] = job
	return job, nil
}

func (queries *Memory) SetNegativesReturned(_ context.Context, arg gen.SetNegativesReturnedParams) (gen.Processing, error) {
	job := queries.Jobs[arg.ID]
	job.NegativesReturnedAt = arg.NegativesReturnedAt
	queries.Jobs[arg.ID] = job
	return job, nil
}

func (queries *Memory) ListRollProcessing(_ context.Context, rollID uuid.UUID) ([]gen.ListRollProcessingRow, error) {
	var rows []gen.ListRollProcessingRow
	for _, job := range queries.Jobs {
		if job.RollID == rollID && !job.DeletedAt.Valid {
			rows = append(rows, gen.ListRollProcessingRow{
				ID: job.ID, RollID: job.RollID, LabID: job.LabID, Type: job.Type, Process: job.Process, SentAt: job.SentAt,
				ScansReceivedAt: job.ScansReceivedAt, NegativesReturnedAt: job.NegativesReturnedAt, Price: job.Price, Notes: job.Notes,
			})
		}
	}
	return rows, nil
}

func (queries *Memory) UpsertFrame(_ context.Context, arg gen.UpsertFrameParams) (gen.Frame, error) {
	for _, frame := range queries.Frames {
		if frame.RollID == arg.RollID && frame.Number == arg.Number {
			return frame, nil
		}
	}
	frame := gen.Frame{ID: arg.ID, RollID: arg.RollID, Number: arg.Number}
	queries.Frames[arg.ID] = frame
	return frame, nil
}

func (queries *Memory) GetScanSlot(_ context.Context, arg gen.GetScanSlotParams) (gen.Scan, error) {
	for _, scan := range queries.Scans {
		if !scan.DeletedAt.Valid && scan.ProcessingID == arg.ProcessingID && scan.FrameID == arg.FrameID && scan.Scanner == arg.Scanner {
			return scan, nil
		}
	}
	return gen.Scan{}, pgx.ErrNoRows
}

func (queries *Memory) InsertScan(_ context.Context, arg gen.InsertScanParams) (gen.Scan, error) {
	scan := gen.Scan{
		ID: arg.ID, ProcessingID: arg.ProcessingID, FrameID: arg.FrameID, Scanner: arg.Scanner,
		FileKey: arg.FileKey, FileName: arg.FileName, ContentType: arg.ContentType, SizeBytes: arg.SizeBytes,
	}
	queries.Scans[arg.ID] = scan
	return scan, nil
}

func (queries *Memory) ReplaceScan(_ context.Context, arg gen.ReplaceScanParams) (gen.Scan, error) {
	scan := queries.Scans[arg.ID]
	scan.FileKey, scan.FileName, scan.ContentType, scan.SizeBytes = arg.FileKey, arg.FileName, arg.ContentType, arg.SizeBytes
	queries.Scans[arg.ID] = scan
	return scan, nil
}

// ---- in-memory file storage ----

type MemoryFiles struct {
	Contents map[string]string
}

func NewMemoryFiles() *MemoryFiles { return &MemoryFiles{Contents: map[string]string{}} }

func (files *MemoryFiles) Put(key string, content io.Reader) (int64, error) {
	data, err := io.ReadAll(content)
	if err != nil {
		return 0, err
	}
	files.Contents[key] = string(data)
	return int64(len(data)), nil
}

func (files *MemoryFiles) Open(key string) (io.ReadCloser, int64, error) {
	data := files.Contents[key]
	return io.NopCloser(strings.NewReader(data)), int64(len(data)), nil
}

func (files *MemoryFiles) Delete(key string) error {
	delete(files.Contents, key)
	return nil
}

// ---- listings ----

func (queries *Memory) ListCameras(context.Context) ([]gen.Camera, error) {
	var cameras []gen.Camera
	for _, camera := range queries.Cameras {
		if !camera.DeletedAt.Valid {
			cameras = append(cameras, camera)
		}
	}
	sort.Slice(cameras, func(left, right int) bool { return cameras[left].Brand < cameras[right].Brand })
	return cameras, nil
}

func (queries *Memory) ListLenses(_ context.Context, arg gen.ListLensesParams) ([]gen.Lens, error) {
	var lenses []gen.Lens
	for _, lens := range queries.Lenses {
		if !lens.DeletedAt.Valid && (!arg.ActiveOnly || lens.IsActive) {
			lenses = append(lenses, lens)
		}
	}
	return lenses, nil
}

func (queries *Memory) ListFilmStocks(_ context.Context, _ *string) ([]gen.FilmStock, error) {
	var stocks []gen.FilmStock
	for _, stock := range queries.Stocks {
		if !stock.DeletedAt.Valid {
			stocks = append(stocks, stock)
		}
	}
	sort.Slice(stocks, func(left, right int) bool { return stocks[left].Name < stocks[right].Name })
	return stocks, nil
}

// ---- rolls ----

func (queries *Memory) InsertRoll(_ context.Context, arg gen.InsertRollParams) (gen.Roll, error) {
	roll := gen.Roll{
		ID: arg.ID, FilmStockID: arg.FilmStockID, Format: arg.Format, Exposures: arg.Exposures, Status: "in_stock",
		Price: arg.Price, ExpiryYear: arg.ExpiryYear, ExpiryMonth: arg.ExpiryMonth,
	}
	queries.Rolls[arg.ID] = roll
	return roll, nil
}

func (queries *Memory) UpdateRoll(_ context.Context, arg gen.UpdateRollParams) (gen.Roll, error) {
	roll := queries.Rolls[arg.ID]
	roll.FilmStockID, roll.Format, roll.Exposures, roll.Price = arg.FilmStockID, arg.Format, arg.Exposures, arg.Price
	roll.ExpiryYear, roll.ExpiryMonth, roll.ShotIso = arg.ExpiryYear, arg.ExpiryMonth, arg.ShotIso
	roll.StartedAt, roll.FinishedAt, roll.Description = arg.StartedAt, arg.FinishedAt, arg.Description
	queries.Rolls[arg.ID] = roll
	return roll, nil
}

func (queries *Memory) LoadRoll(_ context.Context, arg gen.LoadRollParams) (gen.Roll, error) {
	roll := queries.Rolls[arg.ID]
	roll.Status, roll.CameraID, roll.StartedAt, roll.ShotIso = "in_camera", arg.CameraID, arg.StartedAt, arg.ShotIso
	queries.Rolls[arg.ID] = roll
	return roll, nil
}

func (queries *Memory) FinishRoll(_ context.Context, arg gen.FinishRollParams) (gen.Roll, error) {
	roll := queries.Rolls[arg.ID]
	roll.Status, roll.FinishedAt = "done_shooting", arg.FinishedAt
	queries.Rolls[arg.ID] = roll
	return roll, nil
}

func (queries *Memory) ListRollSummaries(_ context.Context, arg gen.ListRollSummariesParams) ([]gen.ListRollSummariesRow, error) {
	var rows []gen.ListRollSummariesRow
	for _, roll := range queries.Rolls {
		if roll.DeletedAt.Valid || (arg.RollID != nil && *arg.RollID != roll.ID) {
			continue
		}
		if arg.Status != nil && *arg.Status != roll.Status {
			continue
		}
		stock := queries.Stocks[roll.FilmStockID]
		rows = append(rows, gen.ListRollSummariesRow{
			ID: roll.ID, FilmStockID: roll.FilmStockID, CameraID: roll.CameraID, Format: roll.Format, Exposures: roll.Exposures,
			Status: roll.Status, ShotIso: roll.ShotIso, ExpiryYear: roll.ExpiryYear, ExpiryMonth: roll.ExpiryMonth, Price: roll.Price,
			StartedAt: roll.StartedAt, FinishedAt: roll.FinishedAt, StockBrand: stock.Brand, StockName: stock.Name, BoxIso: stock.BoxIso,
		})
	}
	sort.Slice(rows, func(left, right int) bool { return rows[left].ID.String() < rows[right].ID.String() })
	return rows, nil
}

func (queries *Memory) ListRollLenses(_ context.Context, rollID uuid.UUID) ([]gen.Lens, error) {
	var lenses []gen.Lens
	for _, link := range queries.RollLenses {
		if link.RollID == rollID {
			lenses = append(lenses, queries.Lenses[link.LensID])
		}
	}
	return lenses, nil
}

func (queries *Memory) InsertRollLens(_ context.Context, arg gen.InsertRollLensParams) error {
	for _, link := range queries.RollLenses {
		if link.RollID == arg.RollID && link.LensID == arg.LensID {
			return nil
		}
	}
	queries.RollLenses = append(queries.RollLenses, gen.RollLens{RollID: arg.RollID, LensID: arg.LensID})
	return nil
}

func (queries *Memory) DeleteRollLensesExcept(_ context.Context, arg gen.DeleteRollLensesExceptParams) error {
	keep := map[uuid.UUID]bool{}
	for _, id := range arg.Column2 {
		keep[id] = true
	}
	var remaining []gen.RollLens
	for _, link := range queries.RollLenses {
		if link.RollID != arg.RollID || keep[link.LensID] {
			remaining = append(remaining, link)
		}
	}
	queries.RollLenses = remaining
	return nil
}

func (queries *Memory) DeleteCameraLensesExcept(_ context.Context, arg gen.DeleteCameraLensesExceptParams) error {
	keep := map[uuid.UUID]bool{}
	for _, id := range arg.Column2 {
		keep[id] = true
	}
	var remaining []gen.CameraLens
	for _, link := range queries.Links {
		if link.CameraID != arg.CameraID || keep[link.LensID] || queries.Lenses[link.LensID].IsBuiltIn {
			remaining = append(remaining, link)
		}
	}
	queries.Links = remaining
	return nil
}

func (queries *Memory) RollSpend(context.Context, uuid.UUID) (gen.RollSpendRow, error) {
	return queries.Spend, nil
}

func (queries *Memory) ListFrames(_ context.Context, rollID uuid.UUID) ([]gen.Frame, error) {
	var frames []gen.Frame
	for _, frame := range queries.Frames {
		if frame.RollID == rollID {
			frames = append(frames, frame)
		}
	}
	sort.Slice(frames, func(left, right int) bool { return frames[left].Number < frames[right].Number })
	return frames, nil
}

func (queries *Memory) SetFrameNotes(_ context.Context, arg gen.SetFrameNotesParams) (gen.Frame, error) {
	frame := queries.Frames[arg.ID]
	frame.Notes = arg.Notes
	queries.Frames[arg.ID] = frame
	return frame, nil
}

func (queries *Memory) ListRollScans(_ context.Context, rollID uuid.UUID) ([]gen.ListRollScansRow, error) {
	var rows []gen.ListRollScansRow
	for _, scan := range queries.Scans {
		frame := queries.Frames[scan.FrameID]
		if frame.RollID == rollID && !scan.DeletedAt.Valid {
			rows = append(rows, gen.ListRollScansRow{
				ID: scan.ID, ProcessingID: scan.ProcessingID, FrameID: scan.FrameID, Scanner: scan.Scanner, FrameNumber: frame.Number,
			})
		}
	}
	return rows, nil
}

func (queries *Memory) ListProcessingScans(_ context.Context, arg gen.ListProcessingScansParams) ([]gen.ListProcessingScansRow, error) {
	var rows []gen.ListProcessingScansRow
	for _, scan := range queries.Scans {
		if scan.DeletedAt.Valid || scan.ProcessingID != arg.ProcessingID || (arg.Scanner != nil && *arg.Scanner != scan.Scanner) {
			continue
		}
		rows = append(rows, gen.ListProcessingScansRow{
			ID: scan.ID, ProcessingID: scan.ProcessingID, FrameID: scan.FrameID, Scanner: scan.Scanner, FileKey: scan.FileKey,
			FileName: scan.FileName, ContentType: scan.ContentType, SizeBytes: scan.SizeBytes, FrameNumber: queries.Frames[scan.FrameID].Number,
		})
	}
	sort.Slice(rows, func(left, right int) bool {
		if rows[left].FrameNumber != rows[right].FrameNumber {
			return rows[left].FrameNumber < rows[right].FrameNumber
		}
		return rows[left].Scanner < rows[right].Scanner
	})
	return rows, nil
}

func (queries *Memory) GetScan(_ context.Context, id uuid.UUID) (gen.Scan, error) {
	scan, found := queries.Scans[id]
	if !found || scan.DeletedAt.Valid {
		return gen.Scan{}, pgx.ErrNoRows
	}
	return scan, nil
}

// ---- idempotency keys ----

func (queries *Memory) ClaimIdempotencyKey(_ context.Context, key string) (int64, error) {
	if _, seen := queries.Idempotency[key]; seen {
		return 0, nil
	}
	queries.Idempotency[key] = gen.IdempotencyKey{Key: key}
	return 1, nil
}

func (queries *Memory) GetIdempotencyKey(_ context.Context, key string) (gen.IdempotencyKey, error) {
	return queries.Idempotency[key], nil
}

func (queries *Memory) FinishIdempotencyKey(_ context.Context, arg gen.FinishIdempotencyKeyParams) error {
	queries.Idempotency[arg.Key] = gen.IdempotencyKey{Key: arg.Key, Status: arg.Status, Response: arg.Response}
	return nil
}

// ---- labs, processing, inventory ----

func (queries *Memory) GetLab(_ context.Context, id uuid.UUID) (gen.Lab, error) {
	lab, found := queries.Labs[id]
	if !found || lab.DeletedAt.Valid {
		return gen.Lab{}, pgx.ErrNoRows
	}
	return lab, nil
}

func (queries *Memory) InsertProcessing(_ context.Context, arg gen.InsertProcessingParams) (gen.Processing, error) {
	job := gen.Processing{ID: arg.ID, RollID: arg.RollID, LabID: arg.LabID, Type: arg.Type, Process: arg.Process, SentAt: arg.SentAt, Price: arg.Price, Notes: arg.Notes}
	queries.Jobs[arg.ID] = job
	return job, nil
}

func (queries *Memory) UpdateProcessing(_ context.Context, arg gen.UpdateProcessingParams) (gen.Processing, error) {
	job := queries.Jobs[arg.ID]
	job.LabID, job.SentAt, job.Price, job.Notes = arg.LabID, arg.SentAt, arg.Price, arg.Notes
	queries.Jobs[arg.ID] = job
	return job, nil
}

func (queries *Memory) NegativesAtLab(context.Context) ([]gen.NegativesAtLabRow, error) {
	return queries.AtLabRows, nil
}

func (queries *Memory) InventoryRows(context.Context, gen.InventoryRowsParams) ([]gen.InventoryRowsRow, error) {
	return queries.Inventory, nil
}

func (queries *Memory) SoonestExpiries(context.Context, gen.SoonestExpiriesParams) ([]gen.SoonestExpiriesRow, error) {
	return queries.Expiries, nil
}

func (queries *Memory) GetFilmStocksByIDs(_ context.Context, ids []uuid.UUID) ([]gen.FilmStock, error) {
	var stocks []gen.FilmStock
	for _, id := range ids {
		if stock, found := queries.Stocks[id]; found && !stock.DeletedAt.Valid {
			stocks = append(stocks, stock)
		}
	}
	return stocks, nil
}

func (queries *Memory) ListJobFrameNumbers(_ context.Context, jobID uuid.UUID) ([]int32, error) {
	seen := map[int32]bool{}
	var numbers []int32
	for _, scan := range queries.Scans {
		if number := queries.Frames[scan.FrameID].Number; !scan.DeletedAt.Valid && scan.ProcessingID == jobID && !seen[number] {
			seen[number] = true
			numbers = append(numbers, number)
		}
	}
	sort.Slice(numbers, func(left, right int) bool { return numbers[left] < numbers[right] })
	return numbers, nil
}

// ---- deletes ----

func (queries *Memory) CountCameraRolls(_ context.Context, cameraID *uuid.UUID) (int64, error) {
	var count int64
	for _, roll := range queries.Rolls {
		if roll.CameraID != nil && *roll.CameraID == *cameraID {
			count++
		}
	}
	return count, nil
}

var deletedNow = pgtype.Timestamptz{Time: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), Valid: true}

func (queries *Memory) SoftDeleteBuiltInLenses(_ context.Context, cameraID uuid.UUID) error {
	for _, link := range queries.Links {
		if lens, found := queries.Lenses[link.LensID]; found && link.CameraID == cameraID && lens.IsBuiltIn && !lens.DeletedAt.Valid {
			lens.DeletedAt = deletedNow
			queries.Lenses[lens.ID] = lens
		}
	}
	return nil
}

func (queries *Memory) SoftDeleteCamera(_ context.Context, id uuid.UUID) (int64, error) {
	camera, found := queries.Cameras[id]
	if !found || camera.DeletedAt.Valid {
		return 0, nil
	}
	camera.DeletedAt = deletedNow
	queries.Cameras[id] = camera
	return 1, nil
}

func (queries *Memory) LensIsOnRolls(_ context.Context, lensID uuid.UUID) (bool, error) {
	for _, link := range queries.RollLenses {
		if link.LensID == lensID {
			return true, nil
		}
	}
	return false, nil
}

func (queries *Memory) SoftDeleteLens(_ context.Context, id uuid.UUID) (int64, error) {
	lens, found := queries.Lenses[id]
	if !found || lens.DeletedAt.Valid {
		return 0, nil
	}
	lens.DeletedAt = deletedNow
	queries.Lenses[id] = lens
	return 1, nil
}

func (queries *Memory) FilmStockInUse(_ context.Context, id uuid.UUID) (bool, error) {
	for _, roll := range queries.Rolls {
		if roll.FilmStockID == id {
			return true, nil
		}
	}
	for _, stock := range queries.Stocks {
		if !stock.DeletedAt.Valid && stock.BaseStockID != nil && *stock.BaseStockID == id {
			return true, nil
		}
	}
	return false, nil
}

func (queries *Memory) SoftDeleteFilmStock(_ context.Context, id uuid.UUID) (int64, error) {
	stock, found := queries.Stocks[id]
	if !found || stock.DeletedAt.Valid {
		return 0, nil
	}
	stock.DeletedAt = deletedNow
	queries.Stocks[id] = stock
	return 1, nil
}

func (queries *Memory) GetFrame(_ context.Context, id uuid.UUID) (gen.Frame, error) {
	frame, found := queries.Frames[id]
	if !found {
		return gen.Frame{}, pgx.ErrNoRows
	}
	return frame, nil
}

func (queries *Memory) SoftDeleteRoll(_ context.Context, id uuid.UUID) (int64, error) {
	roll, found := queries.Rolls[id]
	if !found || roll.DeletedAt.Valid {
		return 0, nil
	}
	roll.DeletedAt = deletedNow
	queries.Rolls[id] = roll
	return 1, nil
}

func (queries *Memory) SoftDeleteProcessing(_ context.Context, id uuid.UUID) (int64, error) {
	job, found := queries.Jobs[id]
	if !found || job.DeletedAt.Valid {
		return 0, nil
	}
	job.DeletedAt = deletedNow
	queries.Jobs[id] = job
	return 1, nil
}

func (queries *Memory) ProcessingHasScans(_ context.Context, jobID uuid.UUID) (bool, error) {
	for _, scan := range queries.Scans {
		if scan.ProcessingID == jobID && !scan.DeletedAt.Valid {
			return true, nil
		}
	}
	return false, nil
}

func (queries *Memory) SoftDeleteScan(_ context.Context, id uuid.UUID) (int64, error) {
	scan, found := queries.Scans[id]
	if !found || scan.DeletedAt.Valid {
		return 0, nil
	}
	scan.DeletedAt = deletedNow
	queries.Scans[id] = scan
	return 1, nil
}
