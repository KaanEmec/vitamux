package ingest

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// KindNormalizeBatch is the job that normalizes the new raw rows of one batch. Its payload
// is NormalizePayload.
const KindNormalizeBatch = "normalize_batch"

// NormalizePayload is the payload of a KindNormalizeBatch job.
type NormalizePayload struct {
	BatchID uuid.UUID `json:"batch_id"`
}

var batchIDRe = regexp.MustCompile(`^bat_[0-9a-f]{32}$`)

// FormatBatchID returns the bat_<32 hex> form of a batch UUID.
func FormatBatchID(id uuid.UUID) string { return "bat_" + hex.EncodeToString(id[:]) }

// ParseBatchID returns the UUID of a bat_<32 hex> identifier.
func ParseBatchID(s string) (uuid.UUID, error) {
	if !batchIDRe.MatchString(s) {
		return uuid.Nil, errors.New("ingest: malformed batch id")
	}
	return uuid.Parse(strings.TrimPrefix(s, "bat_"))
}

// Accepted is the 202 response of POST /batches (BatchAccepted in api/openapi.yaml).
type Accepted struct {
	BatchID       string        `json:"batch_id"`
	Items         []ItemOutcome `json:"items"`
	Normalization string        `json:"normalization"`
}

// ItemOutcome reports one stored item.
type ItemOutcome struct {
	ExternalKey  string  `json:"external_key"`
	Status       Outcome `json:"status"`
	RawPayloadID string  `json:"raw_payload_id"`
}

// AcceptPush stores a validated push batch inside the caller's transaction: the batch row,
// its raw items, and (when anything is new) a KindNormalizeBatch job. Items that reference a
// blob not uploaded yet fail with *ValidationError.
func AcceptPush(ctx context.Context, q *dbq.Queries, blobs *blob.Store, in BatchInfo, b *Batch) (Accepted, error) {
	items := make([]RawItem, len(b.Items))
	var c checker
	for i, it := range b.Items {
		raw, err := it.Raw()
		if err != nil {
			return Accepted{}, err // Validate accepted it, so this is a bug
		}
		if raw.BlobSHA256 != nil {
			_, err := q.GetBlob(ctx, raw.BlobSHA256)
			if err = db.MapErr(err); err != nil && !errors.Is(err, db.ErrNotFound) {
				return Accepted{}, err
			}
			c.check(err == nil, "/items/"+strconv.Itoa(i)+"/blob_sha256", "no uploaded blob has this hash; upload it to /batches/{id}/blobs first")
		}
		items[i] = raw
	}
	if err := c.err(); err != nil {
		return Accepted{}, err
	}
	ref, err := CreateBatch(ctx, q, in)
	if err != nil {
		return Accepted{}, err
	}
	results, err := StoreRaw(ctx, q, blobs, ref, items)
	if err != nil {
		return Accepted{}, err
	}
	out := Accepted{BatchID: FormatBatchID(ref.ID), Items: make([]ItemOutcome, len(results)), Normalization: "queued"}
	fresh := false
	for i, r := range results {
		out.Items[i] = ItemOutcome{ExternalKey: r.ExternalKey, Status: r.Outcome, RawPayloadID: strconv.FormatInt(r.RawPayloadID, 10)}
		fresh = fresh || r.Outcome != Duplicate
	}
	if fresh {
		_, _, err = jobs.Enqueue(ctx, q, jobs.NewJob{
			Kind: KindNormalizeBatch, ConnectionID: &ref.ConnectionID,
			DedupeKey: KindNormalizeBatch + ":" + ref.ID.String(), Payload: NormalizePayload{BatchID: ref.ID},
		})
	}
	return out, err
}

// BatchStatus is the response of GET /batches/{id} (BatchStatus in api/openapi.yaml).
type BatchStatus struct {
	BatchID       string            `json:"batch_id"`
	ConnectionID  uuid.UUID         `json:"-"`
	ReceivedAt    time.Time         `json:"received_at"`
	Normalization string            `json:"normalization"`
	Items         []BatchItemStatus `json:"items"`
}

// BatchItemStatus is one raw row written by the batch (duplicates wrote none).
type BatchItemStatus struct {
	ExternalKey  string `json:"external_key"`
	RawPayloadID string `json:"raw_payload_id"`
	Status       Status `json:"status"`
}

// GetBatchStatus reports batch id and the processing state of its raw rows. It returns
// db.ErrNotFound for an unknown batch; callers check ConnectionID.
func GetBatchStatus(ctx context.Context, q *dbq.Queries, id uuid.UUID) (BatchStatus, error) {
	b, err := q.GetIngestBatch(ctx, id)
	if err != nil {
		return BatchStatus{}, db.MapErr(err)
	}
	rows, err := q.ListBatchRaw(ctx, id)
	if err != nil {
		return BatchStatus{}, err
	}
	job, err := q.LatestNormalizeJobStatus(ctx, id.String())
	if err = db.MapErr(err); err != nil && !errors.Is(err, db.ErrNotFound) {
		return BatchStatus{}, err
	}
	st := BatchStatus{BatchID: FormatBatchID(b.ID), ConnectionID: b.ConnectionID, ReceivedAt: b.ReceivedAt,
		Items: make([]BatchItemStatus, len(rows))}
	for i, r := range rows {
		st.Items[i] = BatchItemStatus{ExternalKey: r.ExternalKey, RawPayloadID: strconv.FormatInt(r.ID, 10), Status: Status(r.Status)}
	}
	st.Normalization = normalization(st.Items, job)
	return st, nil
}

// normalization aggregates the batch's normalize job (job status, "" if none) and its raw
// rows: the job's own state while it is queued or running, failed when it died or any row
// failed or was quarantined, done otherwise. Rows still stored after the job finished stay
// visible per item.
func normalization(items []BatchItemStatus, job string) string {
	switch job {
	case "queued", "running":
		return job
	case "dead", "cancelled":
		return "failed"
	}
	pending := false
	for _, it := range items {
		switch it.Status {
		case StatusNormalizeFailed, StatusQuarantined:
			return "failed"
		case StatusStored:
			pending = true
		case StatusNormalized:
		}
	}
	if pending && job == "" {
		return "queued" // rows put back to stored for reprocessing by another job kind
	}
	return "done"
}

// heartbeatRecord is what clients.metadata.heartbeat holds: the heartbeat without checkpoints
// (those go to sync_cursors).
type heartbeatRecord struct {
	Client             Client         `json:"client"`
	SentAt             time.Time      `json:"sent_at"`
	ReceivedAt         time.Time      `json:"received_at"`
	PendingFailedUnits int            `json:"pending_failed_units"`
	LastErrorClass     *string        `json:"last_error_class"`
	LastErrorAt        *string        `json:"last_error_at"`
	Streams            []streamRecord `json:"streams"`
}

type streamRecord struct {
	Stream             string  `json:"stream"`
	LastSuccessAt      *string `json:"last_success_at"`
	PendingFailedUnits int     `json:"pending_failed_units"`
	LastErrorClass     *string `json:"last_error_class"`
}

// RecordHeartbeat stores a validated heartbeat of client clientID: the summary in
// clients.metadata.heartbeat, and per stream the checkpoint (sync_cursors.cursor), last
// success (high_watermark) and error class (status degraded, status_reason). A heartbeat
// older than the recorded one changes nothing.
func RecordHeartbeat(ctx context.Context, q *dbq.Queries, clientID, connectionID uuid.UUID, h *Heartbeat, now time.Time) error {
	sent, err := time.Parse(time.RFC3339Nano, h.SentAt)
	if err != nil {
		return err // Validate accepted it, so this is a bug
	}
	rec := heartbeatRecord{Client: h.Client, SentAt: sent, ReceivedAt: now, PendingFailedUnits: h.PendingFailedUnits,
		LastErrorClass: h.LastErrorClass, LastErrorAt: h.LastErrorAt, Streams: make([]streamRecord, len(h.Streams))}
	for i, s := range h.Streams {
		rec.Streams[i] = streamRecord{Stream: s.Stream, LastSuccessAt: s.LastSuccessAt, PendingFailedUnits: s.PendingFailedUnits, LastErrorClass: s.LastErrorClass}
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	n, err := q.SetClientHeartbeat(ctx, dbq.SetClientHeartbeatParams{Heartbeat: data, ID: clientID, SentAt: sent})
	if err != nil || n == 0 {
		return err
	}
	for _, s := range h.Streams {
		p := dbq.UpsertPushCursorParams{ConnectionID: connectionID, Stream: s.Stream, Cursor: s.Checkpoint, Status: "ok", StatusReason: s.LastErrorClass}
		if s.LastErrorClass != nil {
			p.Status = "degraded"
		}
		if s.LastSuccessAt != nil {
			t, err := time.Parse(time.RFC3339Nano, *s.LastSuccessAt)
			if err != nil {
				return err
			}
			p.HighWatermark = &t
		}
		if err := q.UpsertPushCursor(ctx, p); err != nil {
			return err
		}
	}
	return nil
}
