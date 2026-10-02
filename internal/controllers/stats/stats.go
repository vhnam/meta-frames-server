package stats

import (
	"context"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/controllers/roll"
	"meta-frames-server/internal/controllers/scan"
	statssvc "meta-frames-server/internal/services/stats"
)

// Controller serves the stats endpoints.
type Controller struct {
	stats *statssvc.Service
}

// New builds the controller around its service.
func New(stats *statssvc.Service) *Controller { return &Controller{stats: stats} }

func toAPIRankedItems(items []statssvc.RankedItem) []api.RankedItem {
	mapped := make([]api.RankedItem, len(items))
	for index, item := range items {
		mapped[index] = api.RankedItem{Id: item.ID, Label: item.Label, Rolls: int(item.Rolls)}
	}
	return mapped
}

func (controller *Controller) SearchRolls(ctx context.Context, request api.SearchRollsRequestObject) (api.SearchRollsResponseObject, error) {
	hits, err := controller.stats.SearchByFocalLength(ctx, request.Params.FocalLength)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.SearchResult, len(hits))
	for index, hit := range hits {
		mapped[index] = api.SearchResult{Roll: roll.ToAPIRollSummary(hit.Roll), Scans: scan.ToAPIScanRefs(hit.Scans)}
	}
	return api.SearchRolls200JSONResponse(mapped), nil
}

func (controller *Controller) GetGearStats(ctx context.Context, request api.GetGearStatsRequestObject) (api.GetGearStatsResponseObject, error) {
	stats, err := controller.stats.Gear(ctx, request.Params.Year)
	if err != nil {
		return nil, err
	}
	return api.GetGearStats200JSONResponse{Cameras: toAPIRankedItems(stats.Cameras), Lenses: toAPIRankedItems(stats.Lenses)}, nil
}

func (controller *Controller) GetFilmStats(ctx context.Context, request api.GetFilmStatsRequestObject) (api.GetFilmStatsResponseObject, error) {
	groupByBase := request.Params.GroupByBase != nil && *request.Params.GroupByBase
	items, err := controller.stats.Film(ctx, groupByBase)
	if err != nil {
		return nil, err
	}
	return api.GetFilmStats200JSONResponse(toAPIRankedItems(items)), nil
}

func (controller *Controller) GetTimelineStats(ctx context.Context, _ api.GetTimelineStatsRequestObject) (api.GetTimelineStatsResponseObject, error) {
	timelines, err := controller.stats.Timeline(ctx)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.YearTimeline, len(timelines))
	for index, timeline := range timelines {
		mapped[index] = api.YearTimeline{Year: timeline.Year, Months: timeline.Months[:]}
	}
	return api.GetTimelineStats200JSONResponse(mapped), nil
}

func (controller *Controller) GetSpendingStats(ctx context.Context, _ api.GetSpendingStatsRequestObject) (api.GetSpendingStatsResponseObject, error) {
	years, err := controller.stats.Spending(ctx)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.YearSpending, len(years))
	for index, year := range years {
		months := make([]api.MonthSpending, len(year.Months))
		for monthIndex, month := range year.Months {
			months[monthIndex] = api.MonthSpending{
				Month: month.Month, FilmCost: month.FilmCost, ProcessingCost: month.ProcessingCost, Incomplete: month.Incomplete,
			}
		}
		mapped[index] = api.YearSpending{
			Year: year.Year, FilmCost: year.FilmCost, ProcessingCost: year.ProcessingCost, Incomplete: year.Incomplete, Months: months,
		}
	}
	return api.GetSpendingStats200JSONResponse(mapped), nil
}
