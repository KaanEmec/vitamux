package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/export"
)

// Exports (J10.6; docs/architecture/api.md#exports). They need the admin scope: an export is
// every health row, raw payload, rule and audit event at once, more than read:health and
// read:config together, so a narrower key cannot take the whole instance in one call.
func (rt *router) exportRoutes() {
	admin := scope(auth.Admin)
	rt.handle("POST /api/v1/exports", admin, rt.ops.CreateExport)
	rt.handle("GET /api/v1/exports/{id}", admin, rt.ops.GetExport)
	rt.handle("GET /api/v1/exports/{id}/download", admin, rt.ops.DownloadExport)
}

func (o *owner) exportsReady() error {
	if o.opts.DB == nil || o.opts.Blobs == nil {
		return problemErr(CodeUnavailable, "exports need the database, the master key and the data directory")
	}
	return nil
}

// CreateExport queues an export. Idempotency-Key is accepted; a repeated request while the
// first export is queued or running answers 409.
func (o *owner) CreateExport(ctx context.Context, req oapi.CreateExportRequestObject) (oapi.CreateExportResponseObject, error) {
	if err := o.exportsReady(); err != nil {
		return nil, err
	}
	b := req.Body
	if !b.Format.Valid() {
		return nil, problemErr(CodeValidationFailed, "unknown format", FieldError{Pointer: "/format", Detail: "must be ndjson or csv"})
	}
	if b.Scope != nil && len(*b.Scope) > 0 {
		return nil, problemErr(CodeValidationFailed, "scoped exports are not supported yet", FieldError{Pointer: "/scope", Detail: "omit it to export everything"})
	}
	p := auth.PrincipalFrom(ctx)
	e, err := export.Create(ctx, o.opts.DB, p.UserID, string(b.Format), b.IncludeRaw != nil && *b.IncludeRaw, p.Actor())
	if errors.Is(err, export.ErrRunning) {
		return nil, problemErr(CodeConflict, "an export is already queued or running")
	}
	if err != nil {
		return nil, err
	}
	return oapi.CreateExport202JSONResponse(exportBody(e)), nil
}

// GetExport answers the status; a finished export carries a fresh one-time download URL.
func (o *owner) GetExport(ctx context.Context, req oapi.GetExportRequestObject) (oapi.GetExportResponseObject, error) {
	if err := o.exportsReady(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, db.ErrNotFound
	}
	e, err := export.Get(ctx, o.opts.DB, auth.PrincipalFrom(ctx).UserID, id)
	if err != nil {
		return nil, err
	}
	return oapi.GetExport200JSONResponse(exportBody(e)), nil
}

// DownloadExport streams the zip once per token.
func (o *owner) DownloadExport(ctx context.Context, req oapi.DownloadExportRequestObject) (oapi.DownloadExportResponseObject, error) {
	if err := o.exportsReady(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, db.ErrNotFound
	}
	rc, size, err := export.Open(ctx, o.opts.DB, o.opts.Blobs, auth.PrincipalFrom(ctx).UserID, id, req.Params.Token)
	if errors.Is(err, export.ErrBadToken) {
		return nil, problemErr(CodeForbidden, "the download token is invalid, expired or already used; get the export again for a new one")
	}
	if err != nil {
		return nil, err
	}
	return zipDownload{body: rc, size: size, name: "vitamux-export-" + id.String() + ".zip"}, nil
}

func exportBody(e export.Export) oapi.Export {
	format, raw := oapi.ExportFormat(e.Format), e.IncludeRaw
	out := oapi.Export{ID: e.ID.String(), Status: oapi.ExportStatus(e.Status), Format: &format, IncludeRaw: &raw,
		CreatedAt: e.CreatedAt, FinishedAt: e.FinishedAt}
	if e.SizeBytes != nil {
		n := int(*e.SizeBytes)
		out.SizeBytes = &n
	}
	if e.DownloadToken != "" {
		u := "/api/v1/exports/" + e.ID.String() + "/download?token=" + e.DownloadToken
		out.DownloadURL = &u
	}
	return out
}

// zipDownload is the 200 response: an attachment that may take longer than the server's
// write timeout to send.
type zipDownload struct {
	body io.ReadCloser
	size int64
	name string
}

func (z zipDownload) VisitDownloadExportResponse(w http.ResponseWriter) error {
	defer z.body.Close()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{}) // a large zip outlasts WriteTimeout
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+z.name+`"`)
	if z.size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(z.size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, z.body)
	return err
}
