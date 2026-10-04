package api

import (
	"context"
	"slices"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

// maxCoverageDays bounds a coverage request (a leap year) so the matrix stays small whatever
// metrics it covers.
const maxCoverageDays = 366

// GetCoverage answers the source × day matrix (J10.5): per metric and provider, the share of
// each local day's hours that have data in source_hourly_aggregates. Without a metric filter it
// covers every metric that has data in the range. Aggregates the rebuild job has not consumed
// yet are not reflected.
func (o *owner) GetCoverage(ctx context.Context, req oapi.GetCoverageRequestObject) (oapi.GetCoverageResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	from, to := req.Params.StartDate.Time, req.Params.EndDate.Time
	days := int(to.Sub(from).Hours()/24) + 1
	switch {
	case to.Before(from):
		return nil, problemErr(CodeValidationFailed, "invalid range", FieldError{Pointer: "/end_date", Detail: "must not be before start_date"})
	case days > maxCoverageDays:
		return nil, problemErr(CodeValidationFailed, "range too long", FieldError{Pointer: "/end_date", Detail: "at most 366 days from start_date"})
	}
	var metrics []string
	for _, m := range ptrVal(req.Params.Metric) {
		if _, ok := catalog.Lookup(m); !ok {
			return nil, problemErr(CodeValidationFailed, "unknown metric", FieldError{Pointer: "/metric", Detail: "not a catalogue metric code"})
		}
		if !slices.Contains(metrics, m) {
			metrics = append(metrics, m)
		}
	}
	userID := auth.PrincipalFrom(ctx).UserID
	rows, err := d.Q().CoverageHours(ctx, dbq.CoverageHoursParams{UserID: userID,
		FromAt: from.AddDate(0, 0, -2), ToAt: to.AddDate(0, 0, 3), FromDate: from, ToDate: to, Metrics: metrics, Origins: ptrVal(req.Params.Origin)})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := oapi.GetCoverage200JSONResponse{StartDate: req.Params.StartDate, EndDate: req.Params.EndDate, Rows: []oapi.CoverageRow{}}
	if len(rows) == 0 {
		return out, nil
	}
	tl, err := normalize.NewPeriods(d).Timeline(ctx, userID)
	if err != nil {
		return nil, err
	}
	dayHours := make([]float64, days) // hours in each local day: 23 or 25 on DST days
	for i := range dayHours {
		dayHours[i] = 24
		if w, err := resolve.LocalDay(from.AddDate(0, 0, i), tl); err == nil {
			dayHours[i] = w.End.Sub(w.Start).Hours()
		}
	}
	var cur *oapi.CoverageRow // rows are ordered by metric, source, date
	for _, r := range rows {
		if cur == nil || cur.Metric != r.Metric || cur.Source != r.Source {
			out.Rows = append(out.Rows, oapi.CoverageRow{Metric: r.Metric, Source: r.Source, Days: make([]float64, days)})
			cur = &out.Rows[len(out.Rows)-1]
		}
		if i := int(r.LocalDate.Sub(from).Hours() / 24); i >= 0 && i < days {
			cur.Days[i] = min(1, float64(r.Hours)/dayHours[i])
		}
	}
	return out, nil
}
