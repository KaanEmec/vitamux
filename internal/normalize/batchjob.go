package normalize

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// Job kinds (docs/architecture/reliability.md#job-queue).
// normalize_batch is ingest.KindNormalizeBatch with an ingest.NormalizePayload.
const KindReprocess = "reprocess"

// Summary counts what a job did over its payloads.
type Summary struct {
	Normalized  int `json:"normalized"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`     // quarantined, superseded raw, or already at the current version
	Inserted    int `json:"inserted"`    // canonical rows
	Superseded  int `json:"superseded"`  // canonical rows replaced by changed ones
	Reversioned int `json:"reversioned"` // unchanged rows that only took the new normalizer version
	Unchanged   int `json:"unchanged"`
	Deleted     int `json:"deleted"`
}

func (s *Summary) add(r Result) {
	switch r.Outcome {
	case Normalized:
		s.Normalized++
	case Failed:
		s.Failed++
	default:
		s.Skipped++
	}
	s.Inserted += r.Stats.Inserted
	s.Superseded += r.Stats.Superseded
	s.Reversioned += r.Stats.Reversioned
	s.Unchanged += r.Stats.Unchanged
	s.Deleted += r.Stats.Deleted
}

func (s Summary) String() string {
	return fmt.Sprintf("%d normalized, %d failed, %d skipped; rows: %d new, %d superseded, %d re-versioned only, %d unchanged, %d deleted",
		s.Normalized, s.Failed, s.Skipped, s.Inserted, s.Superseded, s.Reversioned, s.Unchanged, s.Deleted)
}

// BatchJob returns the KindNormalizeBatch handler. It normalizes the batch's payloads that are
// still stored; progress lives in the raw status, so a retry continues where it stopped.
// Payload failures do not fail the job (they are visible on the raw rows); a database error on
// one payload lets the others finish, then fails the job so it is retried.
func (p *Processor) BatchJob() jobs.Handler {
	return func(ctx context.Context, j jobs.Job) error {
		var bp ingest.NormalizePayload
		if err := json.Unmarshal(j.Payload, &bp); err != nil || bp.BatchID == uuid.Nil {
			return jobs.Permanent(fmt.Errorf("normalize_batch payload: need a batch_id"))
		}
		vers, err := p.versions(ctx)
		if err != nil {
			return err
		}
		ids, err := p.DB.Q().ListBatchPendingRaw(ctx, bp.BatchID)
		if err != nil {
			return db.MapErr(err)
		}
		var sum Summary
		var first error
		for _, id := range ids {
			res, err := p.Process(ctx, id, vers)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				p.Log.Warn("normalize payload", "raw_payload_id", id, "err", err)
				if first == nil {
					first = err
				}
				continue
			}
			sum.add(res)
		}
		p.Log.Info("batch normalized", "batch_id", bp.BatchID, "payloads", len(ids), "summary", sum.String())
		return first
	}
}

// ReprocessPayload selects what a reprocess job re-normalizes: the newest raw version of each
// record, fetched in [Since, Until), matching Stream and Normalizer. Empty fields select all.
type ReprocessPayload struct {
	Normalizer string     `json:"normalizer,omitempty"` // normalizer ID
	Stream     string     `json:"stream,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	Until      *time.Time `json:"until,omitempty"`
}

const (
	reprocessPage   = 500
	checkpointEvery = 100
)

// checkSelection rejects a selection naming a normalizer this build does not have.
func (p *Processor) checkSelection(sel ReprocessPayload) error {
	if _, ok := p.Registry.Get(sel.Normalizer); sel.Normalizer != "" && !ok {
		return fmt.Errorf("unknown normalizer %q", sel.Normalizer)
	}
	return nil
}

// reprocessState is the checkpoint of a reprocess job; Done marks a finished run.
type reprocessState struct {
	After int64   `json:"after"`
	Sum   Summary `json:"summary"`
	Done  bool    `json:"done,omitempty"`
}

// each calls fn with every payload id the job would (re)process, in id order, after id after.
// A payload qualifies when a normalizer accepts it (and is the selected one) and its last
// attempt was not already made by the current version of that normalizer, so a second run
// selects nothing. scanned counts the candidates looked at.
func (p *Processor) each(ctx context.Context, sel ReprocessPayload, vers map[string]int32, after int64,
	fn func(id int64) error) (scanned int, err error) {
	for {
		page, err := p.DB.Q().ListReprocessCandidates(ctx, dbq.ListReprocessCandidatesParams{
			After: after, Stream: sel.Stream, Since: sel.Since, Until: sel.Until, MaxRows: reprocessPage})
		if err != nil {
			return scanned, db.MapErr(err)
		}
		for _, c := range page {
			scanned++
			fp := ""
			if c.ShapeFingerprint != nil {
				fp = *c.ShapeFingerprint
			}
			n, err := p.Registry.For(c.Stream, fp)
			if err != nil || (sel.Normalizer != "" && n.ID() != sel.Normalizer) {
				continue // not ours; a payload without a normalizer stays as ingest left it
			}
			if c.NormalizerVersionID != nil && *c.NormalizerVersionID == vers[n.ID()] {
				continue // already attempted by this version
			}
			if err := fn(c.ID); err != nil {
				return scanned, err
			}
		}
		if len(page) < reprocessPage {
			return scanned, nil
		}
		after = page[len(page)-1].ID
	}
}

// CountReprocess reports how many payloads a reprocess job with sel would process now, out of
// how many it scanned.
func (p *Processor) CountReprocess(ctx context.Context, sel ReprocessPayload) (selected, scanned int, err error) {
	if err := p.checkSelection(sel); err != nil {
		return 0, 0, err
	}
	vers, err := p.versions(ctx)
	if err != nil {
		return 0, 0, err
	}
	scanned, err = p.each(ctx, sel, vers, 0, func(int64) error { selected++; return nil })
	return selected, scanned, err
}

// EnqueueReprocess queues a reprocess job for sel. An identical job still queued or running is
// returned instead of a second one.
func EnqueueReprocess(ctx context.Context, d *db.DB, sel ReprocessPayload) (id uuid.UUID, created bool, err error) {
	key, err := json.Marshal(sel)
	if err != nil {
		return uuid.Nil, false, err
	}
	return jobs.Enqueue(ctx, d.Q(), jobs.NewJob{Kind: KindReprocess, Priority: jobs.PriorityLow,
		DedupeKey: KindReprocess + ":" + string(key), Payload: sel})
}

// ReprocessJob returns the KindReprocess handler. It re-normalizes the selected raw payloads in
// id order, one transaction each. Output the writer finds unchanged only gets the new normalizer
// version (Summary.Reversioned); changed records are superseded. It checkpoints every few payloads
// and ends with the Summary as the checkpoint, which `vitamux reprocess --wait` prints.
func (p *Processor) ReprocessJob() jobs.Handler {
	return func(ctx context.Context, j jobs.Job) error {
		var sel ReprocessPayload
		if err := json.Unmarshal(j.Payload, &sel); err != nil {
			return jobs.Permanent(fmt.Errorf("reprocess payload: %w", err))
		}
		var st reprocessState
		if len(j.Checkpoint) > 0 {
			if err := json.Unmarshal(j.Checkpoint, &st); err != nil {
				return jobs.Permanent(fmt.Errorf("reprocess checkpoint: %w", err))
			}
		}
		if err := p.checkSelection(sel); err != nil {
			return jobs.Permanent(err)
		}
		vers, err := p.versions(ctx)
		if err != nil {
			return err
		}
		var first error
		_, err = p.each(ctx, sel, vers, st.After, func(id int64) error {
			res, err := p.Process(ctx, id, vers)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				p.Log.Warn("reprocess payload", "raw_payload_id", id, "err", err)
				if first == nil {
					first = err
				}
				return nil
			}
			st.Sum.add(res)
			if first == nil { // after a database error the next run starts at that payload
				st.After = id
			}
			if n := st.Sum.Normalized + st.Sum.Failed + st.Sum.Skipped; n%checkpointEvery == 0 {
				return j.SaveCheckpoint(ctx, st)
			}
			return nil
		})
		if err != nil {
			return err
		}
		p.Log.Info("reprocess finished", "normalizer", sel.Normalizer, "stream", sel.Stream, "summary", st.Sum.String())
		if first != nil {
			return first
		}
		st.Done = true
		return j.SaveCheckpoint(ctx, st)
	}
}
