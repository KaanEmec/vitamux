package api

import (
	"context"
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Event documents (J22.17, docs/adr/0024-watch-data.md#read-endpoints): an ECG waveform and a
// workout route are JSON documents in the blob store. They are served as stored, with their
// SHA-256 as ETag; the owner middleware keeps them out of caches like every health response.

// blobDoc is a stored document written as is.
type blobDoc struct{ body, sum []byte }

func (d blobDoc) write(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(d.body)))
	h.Set("ETag", `"`+hex.EncodeToString(d.sum)+`"`)
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}

type waveformResponse struct{ blobDoc }

func (r waveformResponse) VisitGetEventWaveformResponse(w http.ResponseWriter) error {
	return r.write(w)
}

type routeResponse struct{ blobDoc }

func (r routeResponse) VisitGetWorkoutRouteResponse(w http.ResponseWriter) error { return r.write(w) }

// document reads the blob sum; nil means the row has none (404).
func (o *owner) document(sum []byte) (blobDoc, error) {
	if len(sum) == 0 {
		return blobDoc{}, db.ErrNotFound
	}
	if o.opts.Blobs == nil {
		return blobDoc{}, problemErr(CodeUnavailable, "waveforms and routes need the data directory")
	}
	body, err := o.opts.Blobs.Get(sum) // verified against the hash; a missing file is a server fault
	if err != nil {
		return blobDoc{}, err
	}
	return blobDoc{body: body, sum: sum}, nil
}

// GetEventWaveform returns the waveform of an ecg_recording event, any version.
func (o *owner) GetEventWaveform(ctx context.Context, req oapi.GetEventWaveformRequestObject) (oapi.GetEventWaveformResponseObject, error) {
	if o.opts.DB == nil {
		return nil, problemErr(CodeUnavailable, "the database is not ready")
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, db.ErrNotFound
	}
	row, err := o.opts.DB.Q().GetEventFile(ctx, dbq.GetEventFileParams{UserID: auth.PrincipalFrom(ctx).UserID, ID: id})
	if err != nil {
		return nil, db.MapErr(err)
	}
	if e, _ := catalog.LookupEvent(row.Code); e.File != catalog.FileWaveform {
		return nil, db.ErrNotFound
	}
	doc, err := o.document(row.FileBlobSha256)
	if err != nil {
		return nil, err
	}
	return waveformResponse{doc}, nil
}

// GetWorkoutRoute returns the route of a workout: the workout_route event naming it, else one
// of its connection and origin inside it (dbq GetWorkoutRouteFile).
func (o *owner) GetWorkoutRoute(ctx context.Context, req oapi.GetWorkoutRouteRequestObject) (oapi.GetWorkoutRouteResponseObject, error) {
	if o.opts.DB == nil {
		return nil, problemErr(CodeUnavailable, "the database is not ready")
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, db.ErrNotFound
	}
	sum, err := o.opts.DB.Q().GetWorkoutRouteFile(ctx, dbq.GetWorkoutRouteFileParams{UserID: auth.PrincipalFrom(ctx).UserID, ID: id})
	if err != nil {
		return nil, db.MapErr(err)
	}
	doc, err := o.document(sum)
	if err != nil {
		return nil, err
	}
	return routeResponse{doc}, nil
}
