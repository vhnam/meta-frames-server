package filmstock

import (
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"

	"github.com/google/uuid"
)

type Input struct {
	Brand       string
	Name        string
	Type        string
	BoxISO      int
	Process     string
	Packaging   string
	StockOrigin *string
	PackOrigin  *string
	Description *string
	BaseStockID *uuid.UUID
}

type InventoryFilter struct {
	Type    *string
	Process *string
	ISO     *int
}

type Detail struct {
	Stock     gen.FilmStock
	BaseStock *gen.FilmStock
	Siblings  []gen.FilmStock
	Derived   []gen.FilmStock
	Warnings  []string
}

type FormatCount struct {
	Format int32
	Count  int32
}

type InventoryItem struct {
	Stock         gen.FilmStock
	Formats       []FormatCount
	SoonestExpiry *domain.ExpiryMonth
}
