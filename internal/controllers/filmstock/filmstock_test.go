package filmstock

import (
	"meta-frames-server/internal/db/gen"
	filmstocksvc "meta-frames-server/internal/services/filmstock"
	"testing"
)

func TestToAPIFilmStockDetailKeepsRelatedListsNonNil(test *testing.T) {
	mapped := toAPIFilmStockDetail(filmstocksvc.Detail{Stock: gen.FilmStock{Brand: "Kodak"}})
	if mapped.Siblings == nil || mapped.Derived == nil {
		test.Fatal("siblings and derived must serialize as [] rather than null")
	}
	if mapped.BaseStock != nil {
		test.Fatal("no base stock expected")
	}
}
