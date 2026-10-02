// Package services wires the business-rule packages together. Each area (camera, lens,
// filmstock, lab, roll, processing, scan, stats) lives in its own subpackage and works with
// plain inputs and views; it knows nothing about HTTP.
package services

import (
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/services/audit"
	"meta-frames-server/internal/services/camera"
	"meta-frames-server/internal/services/filmstock"
	"meta-frames-server/internal/services/lab"
	"meta-frames-server/internal/services/lens"
	"meta-frames-server/internal/services/processing"
	"meta-frames-server/internal/services/roll"
	"meta-frames-server/internal/services/scan"
	"meta-frames-server/internal/services/stats"
	"meta-frames-server/internal/storage"
)

type Services struct {
	Audit      *audit.Service
	Cameras    *camera.Service
	Lenses     *lens.Service
	FilmStocks *filmstock.Service
	Rolls      *roll.Service
	Labs       *lab.Service
	Processing *processing.Service
	Scans      *scan.Service
	Stats      *stats.Service
}

func New(store db.Store, files storage.Store, appClock clock.Clock) *Services {
	return &Services{
		Audit:      audit.New(store),
		Cameras:    camera.New(store),
		Lenses:     lens.New(store),
		FilmStocks: filmstock.New(store),
		Rolls:      roll.New(store, appClock),
		Labs:       lab.New(store),
		Processing: processing.New(store, appClock),
		Scans:      scan.New(store, files, appClock),
		Stats:      stats.New(store),
	}
}
