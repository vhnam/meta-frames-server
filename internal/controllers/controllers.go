// Package controllers adapts HTTP requests to services: it decodes inputs, calls a service
// and maps the result back to the generated API types.
package controllers

import (
	"context"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/controllers/audit"
	"meta-frames-server/internal/controllers/camera"
	"meta-frames-server/internal/controllers/filmstock"
	"meta-frames-server/internal/controllers/lab"
	"meta-frames-server/internal/controllers/lens"
	"meta-frames-server/internal/controllers/processing"
	"meta-frames-server/internal/controllers/roll"
	"meta-frames-server/internal/controllers/scan"
	"meta-frames-server/internal/controllers/stats"
	"meta-frames-server/internal/services"
)

// Each area lives in its own subpackage; local aliases give the embedded fields distinct names.
type (
	auditController      = audit.Controller
	cameraController     = camera.Controller
	labController        = lab.Controller
	lensController       = lens.Controller
	filmStockController  = filmstock.Controller
	processingController = processing.Controller
	rollController       = roll.Controller
	scanController       = scan.Controller
	statsController      = stats.Controller
)

// Controllers implements the generated strict server by embedding one controller per area.
type Controllers struct {
	*HealthController
	*auditController
	*cameraController
	*lensController
	*filmStockController
	*rollController
	*labController
	*processingController
	*scanController
	*statsController
}

var _ api.StrictServerInterface = (*Controllers)(nil)

func New(appServices *services.Services) *Controllers {
	return &Controllers{
		HealthController:     &HealthController{},
		auditController:      audit.New(appServices.Audit),
		cameraController:     camera.New(appServices.Cameras),
		lensController:       lens.New(appServices.Lenses),
		filmStockController:  filmstock.New(appServices.FilmStocks),
		rollController:       roll.New(appServices.Rolls),
		labController:        lab.New(appServices.Labs),
		processingController: processing.New(appServices.Processing),
		scanController:       scan.New(appServices.Scans),
		statsController:      stats.New(appServices.Stats),
	}
}

type HealthController struct{}

func (*HealthController) GetHealth(context.Context, api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	return api.GetHealth200JSONResponse{Status: api.Ok}, nil
}
