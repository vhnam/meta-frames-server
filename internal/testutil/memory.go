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
	"github.com/jackc/pgx/v5/pgconn"
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
	ScanOrders    []gen.ProcessingScanOrder

	RollLenses  []gen.RollLens
	Labs        map[uuid.UUID]gen.Lab
	AtLabRows   []gen.NegativesAtLabRow
	Inventory   []gen.InventoryRowsRow
	Expiries    []gen.SoonestExpiriesRow
	Idempotency map[string]gen.IdempotencyKey
	Spend       gen.RollSpendRow
	Users       map[uuid.UUID]gen.AppUser
	Sessions    map[string]gen.UserSession // by token hash

	HasOpenJob       bool
	HasReceivedScans bool
	InsertedLensIDs  []uuid.UUID
}

func NewMemory() *Memory {
	return &Memory{
		Cameras: map[uuid.UUID]gen.Camera{}, Lenses: map[uuid.UUID]gen.Lens{}, LoadedCameras: map[uuid.UUID]bool{},
		Stocks: map[uuid.UUID]gen.FilmStock{}, Rolls: map[uuid.UUID]gen.Roll{}, Jobs: map[uuid.UUID]gen.Processing{},
		Frames: map[uuid.UUID]gen.Frame{}, Scans: map[uuid.UUID]gen.Scan{}, Idempotency: map[string]gen.IdempotencyKey{}, Labs: map[uuid.UUID]gen.Lab{},
		Users: map[uuid.UUID]gen.AppUser{}, Sessions: map[string]gen.UserSession{},
	}
}

// ---- cameras ----

func (queries *Memory) GetCamera(_ context.Context, arg gen.GetCameraParams) (gen.Camera, error) {
	id := arg.ID
	camera, found := queries.Cameras[id]
	if !found || camera.DeletedAt.Valid {
		return gen.Camera{}, pgx.ErrNoRows
	}
	return camera, nil
}

func (queries *Memory) GetCameraForUpdate(ctx context.Context, arg gen.GetCameraForUpdateParams) (gen.Camera, error) {
	return queries.GetCamera(ctx, gen.GetCameraParams(arg))
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

func (queries *Memory) CameraIsLoaded(_ context.Context, arg gen.CameraIsLoadedParams) (bool, error) {
	cameraID := arg.CameraID
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

func (queries *Memory) ListBuiltInLensLinks(_ context.Context, _ uuid.UUID) ([]gen.CameraLens, error) {
	var links []gen.CameraLens
	for _, link := range queries.Links {
		if lens := queries.Lenses[link.LensID]; lens.IsBuiltIn && !lens.DeletedAt.Valid {
			links = append(links, link)
		}
	}
	return links, nil
}

func (queries *Memory) ListLoadedRolls(_ context.Context, _ uuid.UUID) ([]gen.ListLoadedRollsRow, error) {
	return nil, nil
}

// ---- lenses ----

func (queries *Memory) GetLens(_ context.Context, arg gen.GetLensParams) (gen.Lens, error) {
	id := arg.ID
	lens, found := queries.Lenses[id]
	if !found || lens.DeletedAt.Valid {
		return gen.Lens{}, pgx.ErrNoRows
	}
	return lens, nil
}

func (queries *Memory) GetLensForUpdate(ctx context.Context, arg gen.GetLensForUpdateParams) (gen.Lens, error) {
	return queries.GetLens(ctx, gen.GetLensParams(arg))
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

func (queries *Memory) ListCameraLenses(_ context.Context, arg gen.ListCameraLensesParams) ([]gen.Lens, error) {
	cameraID := arg.CameraID
	var lenses []gen.Lens
	for _, link := range queries.Links {
		if lens := queries.Lenses[link.LensID]; link.CameraID == cameraID && !lens.DeletedAt.Valid {
			lenses = append(lenses, lens)
		}
	}
	return lenses, nil
}

func (queries *Memory) GetLensesByIDs(_ context.Context, arg gen.GetLensesByIDsParams) ([]gen.Lens, error) {
	ids := arg.Ids
	var lenses []gen.Lens
	for _, id := range ids {
		if lens, found := queries.Lenses[id]; found && !lens.DeletedAt.Valid {
			lenses = append(lenses, lens)
		}
	}
	return lenses, nil
}

// ---- film stocks ----

func (queries *Memory) GetFilmStock(_ context.Context, arg gen.GetFilmStockParams) (gen.FilmStock, error) {
	id := arg.ID
	stock, found := queries.Stocks[id]
	if !found || stock.DeletedAt.Valid {
		return gen.FilmStock{}, pgx.ErrNoRows
	}
	return stock, nil
}

func (queries *Memory) GetFilmStockForUpdate(ctx context.Context, arg gen.GetFilmStockForUpdateParams) (gen.FilmStock, error) {
	return queries.GetFilmStock(ctx, gen.GetFilmStockParams(arg))
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

func (queries *Memory) ListDerivedStocks(_ context.Context, arg gen.ListDerivedStocksParams) ([]gen.FilmStock, error) {
	baseID := arg.BaseStockID
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

func (queries *Memory) GetRoll(_ context.Context, arg gen.GetRollParams) (gen.Roll, error) {
	id := arg.ID
	roll, found := queries.Rolls[id]
	if !found || roll.DeletedAt.Valid {
		return gen.Roll{}, pgx.ErrNoRows
	}
	return roll, nil
}

func (queries *Memory) GetRollForUpdate(ctx context.Context, arg gen.GetRollForUpdateParams) (gen.Roll, error) {
	return queries.GetRoll(ctx, gen.GetRollParams(arg))
}

func (queries *Memory) SetRollStatus(_ context.Context, arg gen.SetRollStatusParams) error {
	roll := queries.Rolls[arg.ID]
	roll.Status = arg.Status
	queries.Rolls[arg.ID] = roll
	return nil
}

func (queries *Memory) RollHasOpenJob(_ context.Context, arg gen.RollHasOpenJobParams) (bool, error) {
	return queries.HasOpenJob, nil
}

func (queries *Memory) RollHasScansReceived(_ context.Context, arg gen.RollHasScansReceivedParams) (bool, error) {
	return queries.HasReceivedScans, nil
}

func (queries *Memory) GetProcessing(_ context.Context, arg gen.GetProcessingParams) (gen.Processing, error) {
	id := arg.ID
	job, found := queries.Jobs[id]
	if !found || job.DeletedAt.Valid {
		return gen.Processing{}, pgx.ErrNoRows
	}
	return job, nil
}

func (queries *Memory) GetProcessingForUpdate(ctx context.Context, arg gen.GetProcessingForUpdateParams) (gen.Processing, error) {
	return queries.GetProcessing(ctx, gen.GetProcessingParams(arg))
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

func (queries *Memory) ListRollProcessing(_ context.Context, arg gen.ListRollProcessingParams) ([]gen.ListRollProcessingRow, error) {
	rollID := arg.RollID
	var rows []gen.ListRollProcessingRow
	for _, job := range queries.Jobs {
		if job.RollID == rollID && !job.DeletedAt.Valid {
			rows = append(rows, gen.ListRollProcessingRow{
				ID: job.ID, RollID: job.RollID, LabID: job.LabID, Type: job.Type, Process: job.Process, SentAt: job.SentAt,
				ScansReceivedAt: job.ScansReceivedAt, ScansExpectedAt: job.ScansExpectedAt, NegativesExpectedAt: job.NegativesExpectedAt, NegativesReturnedAt: job.NegativesReturnedAt, Price: job.Price, Notes: job.Notes,
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

func (queries *Memory) ListCameras(_ context.Context, _ uuid.UUID) ([]gen.Camera, error) {
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

func (queries *Memory) ListFilmStocks(_ context.Context, arg gen.ListFilmStocksParams) ([]gen.FilmStock, error) {
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

func (queries *Memory) ListRollLenses(_ context.Context, arg gen.ListRollLensesParams) ([]gen.Lens, error) {
	rollID := arg.RollID
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

func (queries *Memory) RollSpend(_ context.Context, arg gen.RollSpendParams) (gen.RollSpendRow, error) {
	return queries.Spend, nil
}

func (queries *Memory) ListFrames(_ context.Context, arg gen.ListFramesParams) ([]gen.Frame, error) {
	rollID := arg.RollID
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

func (queries *Memory) ListRollScans(_ context.Context, arg gen.ListRollScansParams) ([]gen.ListRollScansRow, error) {
	rollID := arg.RollID
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

func (queries *Memory) GetScan(_ context.Context, arg gen.GetScanParams) (gen.Scan, error) {
	id := arg.ID
	scan, found := queries.Scans[id]
	if !found || scan.DeletedAt.Valid {
		return gen.Scan{}, pgx.ErrNoRows
	}
	return scan, nil
}

// ---- idempotency keys ----

func (queries *Memory) ClaimIdempotencyKey(_ context.Context, arg gen.ClaimIdempotencyKeyParams) (int64, error) {
	key := arg.Key
	if _, seen := queries.Idempotency[key]; seen {
		return 0, nil
	}
	queries.Idempotency[key] = gen.IdempotencyKey{Key: key}
	return 1, nil
}

func (queries *Memory) GetIdempotencyKey(_ context.Context, arg gen.GetIdempotencyKeyParams) (gen.IdempotencyKey, error) {
	key := arg.Key
	return queries.Idempotency[key], nil
}

func (queries *Memory) FinishIdempotencyKey(_ context.Context, arg gen.FinishIdempotencyKeyParams) error {
	queries.Idempotency[arg.Key] = gen.IdempotencyKey{Key: arg.Key, Status: arg.Status, Response: arg.Response}
	return nil
}

// ---- labs, processing, inventory ----

func (queries *Memory) GetLab(_ context.Context, arg gen.GetLabParams) (gen.Lab, error) {
	id := arg.ID
	lab, found := queries.Labs[id]
	if !found || lab.DeletedAt.Valid {
		return gen.Lab{}, pgx.ErrNoRows
	}
	return lab, nil
}

func (queries *Memory) InsertProcessing(_ context.Context, arg gen.InsertProcessingParams) (gen.Processing, error) {
	job := gen.Processing{ID: arg.ID, RollID: arg.RollID, LabID: arg.LabID, Type: arg.Type, Process: arg.Process, SentAt: arg.SentAt, ScansExpectedAt: arg.ScansExpectedAt, NegativesExpectedAt: arg.NegativesExpectedAt, Price: arg.Price, Notes: arg.Notes}
	queries.Jobs[arg.ID] = job
	return job, nil
}

func (queries *Memory) UpdateProcessing(_ context.Context, arg gen.UpdateProcessingParams) (gen.Processing, error) {
	job := queries.Jobs[arg.ID]
	job.LabID, job.SentAt, job.ScansExpectedAt, job.NegativesExpectedAt, job.Price, job.Notes = arg.LabID, arg.SentAt, arg.ScansExpectedAt, arg.NegativesExpectedAt, arg.Price, arg.Notes
	queries.Jobs[arg.ID] = job
	return job, nil
}

func (queries *Memory) NegativesAtLab(_ context.Context, _ uuid.UUID) ([]gen.NegativesAtLabRow, error) {
	return queries.AtLabRows, nil
}

func (queries *Memory) InventoryRows(context.Context, gen.InventoryRowsParams) ([]gen.InventoryRowsRow, error) {
	return queries.Inventory, nil
}

func (queries *Memory) SoonestExpiries(context.Context, gen.SoonestExpiriesParams) ([]gen.SoonestExpiriesRow, error) {
	return queries.Expiries, nil
}

func (queries *Memory) GetFilmStocksByIDs(_ context.Context, arg gen.GetFilmStocksByIDsParams) ([]gen.FilmStock, error) {
	ids := arg.Ids
	var stocks []gen.FilmStock
	for _, id := range ids {
		if stock, found := queries.Stocks[id]; found && !stock.DeletedAt.Valid {
			stocks = append(stocks, stock)
		}
	}
	return stocks, nil
}

func (queries *Memory) ListJobFrameNumbers(_ context.Context, arg gen.ListJobFrameNumbersParams) ([]int32, error) {
	jobID := arg.ProcessingID
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

func (queries *Memory) CountCameraRolls(_ context.Context, arg gen.CountCameraRollsParams) (int64, error) {
	cameraID := arg.CameraID
	var count int64
	for _, roll := range queries.Rolls {
		if roll.CameraID != nil && *roll.CameraID == *cameraID {
			count++
		}
	}
	return count, nil
}

var deletedNow = pgtype.Timestamptz{Time: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), Valid: true}

func (queries *Memory) SoftDeleteBuiltInLenses(_ context.Context, arg gen.SoftDeleteBuiltInLensesParams) error {
	cameraID := arg.CameraID
	for _, link := range queries.Links {
		if lens, found := queries.Lenses[link.LensID]; found && link.CameraID == cameraID && lens.IsBuiltIn && !lens.DeletedAt.Valid {
			lens.DeletedAt = deletedNow
			queries.Lenses[lens.ID] = lens
		}
	}
	return nil
}

func (queries *Memory) SoftDeleteCamera(_ context.Context, arg gen.SoftDeleteCameraParams) (int64, error) {
	id := arg.ID
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

func (queries *Memory) SoftDeleteLens(_ context.Context, arg gen.SoftDeleteLensParams) (int64, error) {
	id := arg.ID
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

func (queries *Memory) SoftDeleteFilmStock(_ context.Context, arg gen.SoftDeleteFilmStockParams) (int64, error) {
	id := arg.ID
	stock, found := queries.Stocks[id]
	if !found || stock.DeletedAt.Valid {
		return 0, nil
	}
	stock.DeletedAt = deletedNow
	queries.Stocks[id] = stock
	return 1, nil
}

func (queries *Memory) GetFrame(_ context.Context, arg gen.GetFrameParams) (gen.Frame, error) {
	id := arg.ID
	frame, found := queries.Frames[id]
	if !found {
		return gen.Frame{}, pgx.ErrNoRows
	}
	return frame, nil
}

func (queries *Memory) SoftDeleteRoll(_ context.Context, arg gen.SoftDeleteRollParams) (int64, error) {
	id := arg.ID
	roll, found := queries.Rolls[id]
	if !found || roll.DeletedAt.Valid {
		return 0, nil
	}
	roll.DeletedAt = deletedNow
	queries.Rolls[id] = roll
	return 1, nil
}

func (queries *Memory) SoftDeleteProcessing(_ context.Context, arg gen.SoftDeleteProcessingParams) (int64, error) {
	id := arg.ID
	job, found := queries.Jobs[id]
	if !found || job.DeletedAt.Valid {
		return 0, nil
	}
	job.DeletedAt = deletedNow
	queries.Jobs[id] = job
	return 1, nil
}

func (queries *Memory) ProcessingHasScans(_ context.Context, arg gen.ProcessingHasScansParams) (bool, error) {
	jobID := arg.ProcessingID
	for _, scan := range queries.Scans {
		if scan.ProcessingID == jobID && !scan.DeletedAt.Valid {
			return true, nil
		}
	}
	return false, nil
}

func (queries *Memory) SoftDeleteScan(_ context.Context, arg gen.SoftDeleteScanParams) (int64, error) {
	id := arg.ID
	scan, found := queries.Scans[id]
	if !found || scan.DeletedAt.Valid {
		return 0, nil
	}
	scan.DeletedAt = deletedNow
	queries.Scans[id] = scan
	return 1, nil
}

// ---- scan orders ----

func (queries *Memory) ListScanOrders(_ context.Context, arg gen.ListScanOrdersParams) ([]gen.ListScanOrdersRow, error) {
	jobIDs := arg.ProcessingIds
	var rows []gen.ListScanOrdersRow
	for _, order := range queries.ScanOrders {
		for _, jobID := range jobIDs {
			if order.ProcessingID != jobID {
				continue
			}
			var count int32
			for _, scan := range queries.Scans {
				if scan.ProcessingID == jobID && scan.Scanner == order.Scanner && !scan.DeletedAt.Valid {
					count++
				}
			}
			rows = append(rows, gen.ListScanOrdersRow{ProcessingID: jobID, Scanner: order.Scanner, HiRes: order.HiRes, ScanCount: count})
		}
	}
	sort.SliceStable(rows, func(left, right int) bool { return rows[left].Scanner < rows[right].Scanner })
	return rows, nil
}

func (queries *Memory) UpsertScanOrder(_ context.Context, arg gen.UpsertScanOrderParams) error {
	for index, order := range queries.ScanOrders {
		if order.ProcessingID == arg.ProcessingID && order.Scanner == arg.Scanner {
			queries.ScanOrders[index].HiRes = arg.HiRes
			return nil
		}
	}
	queries.ScanOrders = append(queries.ScanOrders, gen.ProcessingScanOrder{ProcessingID: arg.ProcessingID, Scanner: arg.Scanner, HiRes: arg.HiRes})
	return nil
}

func (queries *Memory) DeleteScanOrder(_ context.Context, arg gen.DeleteScanOrderParams) (int64, error) {
	for index, order := range queries.ScanOrders {
		if order.ProcessingID == arg.ProcessingID && order.Scanner == arg.Scanner {
			queries.ScanOrders = append(queries.ScanOrders[:index], queries.ScanOrders[index+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (queries *Memory) ScanOrderExists(_ context.Context, arg gen.ScanOrderExistsParams) (bool, error) {
	for _, order := range queries.ScanOrders {
		if order.ProcessingID == arg.ProcessingID && order.Scanner == arg.Scanner {
			return true, nil
		}
	}
	return false, nil
}

// ---- users ----

func (queries *Memory) GetUserByEmail(_ context.Context, email string) (gen.AppUser, error) {
	for _, user := range queries.Users {
		if user.Email == email {
			return user, nil
		}
	}
	return gen.AppUser{}, pgx.ErrNoRows
}

func (queries *Memory) GetUserByRecoverSelector(_ context.Context, selector *string) (gen.AppUser, error) {
	for _, user := range queries.Users {
		if user.RecoverSelector != nil && selector != nil && *user.RecoverSelector == *selector {
			return user, nil
		}
	}
	return gen.AppUser{}, pgx.ErrNoRows
}

func (queries *Memory) GetUserByOAuth2(_ context.Context, arg gen.GetUserByOAuth2Params) (gen.AppUser, error) {
	for _, user := range queries.Users {
		if user.Oauth2Uid != nil && arg.Oauth2Uid != nil && *user.Oauth2Uid == *arg.Oauth2Uid &&
			user.Oauth2Provider != nil && arg.Oauth2Provider != nil && *user.Oauth2Provider == *arg.Oauth2Provider {
			return user, nil
		}
	}
	return gen.AppUser{}, pgx.ErrNoRows
}

func (queries *Memory) InsertUser(ctx context.Context, arg gen.InsertUserParams) (gen.AppUser, error) {
	if _, err := queries.GetUserByEmail(ctx, arg.Email); err == nil {
		return gen.AppUser{}, &pgconn.PgError{Code: "23505"}
	}
	now := pgtype.Timestamptz{Time: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC), Valid: true}
	user := gen.AppUser{
		ID: arg.ID, Email: arg.Email, Name: arg.Name, PasswordHash: arg.PasswordHash,
		Oauth2Provider: arg.Oauth2Provider, Oauth2Uid: arg.Oauth2Uid, CreatedAt: now, UpdatedAt: now,
	}
	queries.Users[arg.ID] = user
	return user, nil
}

func (queries *Memory) UpdateUser(_ context.Context, arg gen.UpdateUserParams) (gen.AppUser, error) {
	user, found := queries.Users[arg.ID]
	if !found {
		return gen.AppUser{}, pgx.ErrNoRows
	}
	user.Name, user.PasswordHash = arg.Name, arg.PasswordHash
	user.RecoverSelector, user.RecoverVerifier, user.RecoverTokenExpiry = arg.RecoverSelector, arg.RecoverVerifier, arg.RecoverTokenExpiry
	user.AttemptCount, user.LastAttempt, user.LockedUntil = arg.AttemptCount, arg.LastAttempt, arg.LockedUntil
	user.Oauth2Provider, user.Oauth2Uid = arg.Oauth2Provider, arg.Oauth2Uid
	queries.Users[arg.ID] = user
	return user, nil
}

// ClaimUnownedData has nothing to hand over: the fake keeps no owners.
func (queries *Memory) ClaimUnownedData(context.Context, uuid.UUID) error { return nil }

// ---- sessions ----

func (queries *Memory) InsertSession(_ context.Context, arg gen.InsertSessionParams) error {
	queries.Sessions[string(arg.TokenHash)] = gen.UserSession{TokenHash: arg.TokenHash, UserID: arg.UserID, ExpiresAt: arg.ExpiresAt}
	return nil
}

func (queries *Memory) GetSessionUser(_ context.Context, tokenHash []byte) (gen.AppUser, error) {
	session, found := queries.Sessions[string(tokenHash)]
	if !found || !session.ExpiresAt.Time.After(time.Now()) {
		return gen.AppUser{}, pgx.ErrNoRows
	}
	user, found := queries.Users[session.UserID]
	if !found {
		return gen.AppUser{}, pgx.ErrNoRows
	}
	return user, nil
}

func (queries *Memory) DeleteSession(_ context.Context, tokenHash []byte) error {
	delete(queries.Sessions, string(tokenHash))
	return nil
}

func (queries *Memory) DeleteUserSessions(_ context.Context, userID uuid.UUID) error {
	for key, session := range queries.Sessions {
		if session.UserID == userID {
			delete(queries.Sessions, key)
		}
	}
	return nil
}

func (queries *Memory) DeleteExpiredSessions(context.Context) error {
	for key, session := range queries.Sessions {
		if !session.ExpiresAt.Time.After(time.Now()) {
			delete(queries.Sessions, key)
		}
	}
	return nil
}
