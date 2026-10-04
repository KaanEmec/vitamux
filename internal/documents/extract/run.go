package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/prompts"
	"github.com/KaanEmec/vitamux/schemas"
)

// Kind is the job that runs one extraction.
const Kind = "extract_document"

const (
	maxAttempts = 3
	// consentSkew tolerates a client clock slightly ahead of the server.
	consentSkew = 5 * time.Minute
	// maxRetryAfter bounds a provider's Retry-After honoured by rescheduling.
	maxRetryAfter = 15 * time.Minute
)

// Start refusals. The API maps them to 422, 403, 409 consent_required and 409 conflict.
var (
	ErrUnknownProvider = errors.New("extract: unknown provider")
	ErrNotConfigured   = errors.New("extract: provider not configured on the server")
	ErrDisabled        = errors.New("extract: provider not enabled in settings")
	ErrConsentRequired = errors.New("extract: consent required")
	ErrConsentMismatch = errors.New("extract: consent does not name the configured provider and model")
	ErrRunning         = errors.New("extract: an extraction of this document is queued or running")
)

// SettingEnabled is the owner setting (boolean) that enables an external provider.
func SettingEnabled(provider string) string { return "documents.external_ai." + provider + ".enabled" }

// Enabled reports whether the user enabled provider; missing means disabled.
func Enabled(ctx context.Context, q *dbq.Queries, user uuid.UUID, provider string) (bool, error) {
	v, err := q.GetUserSetting(ctx, dbq.GetUserSettingParams{UserID: user, Key: SettingEnabled(provider)})
	if errors.Is(db.MapErr(err), db.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var on bool
	if err := json.Unmarshal(v, &on); err != nil {
		return false, fmt.Errorf("extract: setting %s: %w", SettingEnabled(provider), err)
	}
	return on, nil
}

// SetEnabled stores the enablement of an external provider, audited.
func SetEnabled(ctx context.Context, d *db.DB, user uuid.UUID, actor, provider string, on bool) error {
	if provider == Fake || !slices.Contains(Providers, provider) {
		return ErrUnknownProvider
	}
	v, _ := json.Marshal(on)
	return d.Tx(ctx, func(q *dbq.Queries) error {
		if err := q.PutUserSetting(ctx, dbq.PutUserSettingParams{UserID: user, Key: SettingEnabled(provider), Value: v}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "documents.external_ai",
			Detail: map[string]any{"provider": provider, "enabled": on}})
	})
}

// Consent is the owner's acknowledgement that the PDF goes to Provider's Model.
type Consent struct {
	Provider       string    `json:"provider"`
	Model          string    `json:"model"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

// Run is an extraction run as the API shows it; the raw response is never included.
type Run struct {
	ID, DocumentID    uuid.UUID
	Status            string
	Provider          string
	Model             *string
	External          bool
	Consent           *Consent
	SchemaVersion     string
	PromptVersion     string
	ProviderRequestID *string
	DocMeta, Usage    json.RawMessage
	Warnings          []string
	ErrorClass        *string
	RowCount          int
	CreatedBy         string
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
}

// Service starts, runs and lists extractions.
type Service struct {
	db        *db.DB
	blobs     *blob.Store
	docs      *documents.Store
	log       *slog.Logger
	providers map[string]Extractor
	now       func() time.Time
}

// New returns a Service with the fake extractor and the given ones (see Configured).
func New(d *db.DB, blobs *blob.Store, keys *crypto.Keyring, log *slog.Logger, extractors ...Extractor) (*Service, error) {
	f, err := NewFake()
	if err != nil {
		return nil, err
	}
	s := &Service{db: d, blobs: blobs, docs: documents.New(d, blobs, keys), log: log, providers: map[string]Extractor{}, now: time.Now}
	for _, e := range append([]Extractor{f}, extractors...) {
		s.providers[e.ID()] = e
	}
	return s, nil
}

// Provider returns the configured extractor with id, or nil. The UI's consent dialog names
// its Model.
func (s *Service) Provider(id string) Extractor { return s.providers[id] }

type payload struct {
	RunID uuid.UUID `json:"run_id"`
}

// Start checks enablement and consent, then queues an extraction of a live document.
// consent is ignored for the fake provider.
func (s *Service) Start(ctx context.Context, user, docID uuid.UUID, actor, provider string, consent *Consent) (Run, error) {
	ex := s.providers[provider]
	switch {
	case ex == nil && slices.Contains(Providers, provider):
		return Run{}, ErrNotConfigured
	case ex == nil:
		return Run{}, ErrUnknownProvider
	}
	var consentJSON []byte
	if ex.External() {
		on, err := Enabled(ctx, s.db.Q(), user, provider)
		if err != nil {
			return Run{}, err
		}
		if !on {
			return Run{}, ErrDisabled
		}
		switch {
		case consent == nil || consent.AcknowledgedAt.IsZero() || consent.AcknowledgedAt.After(s.now().Add(consentSkew)):
			return Run{}, ErrConsentRequired
		case consent.Provider != ex.ID() || consent.Model != ex.Model():
			return Run{}, ErrConsentMismatch
		}
		consentJSON, _ = json.Marshal(Consent{Provider: consent.Provider, Model: consent.Model, AcknowledgedAt: consent.AcknowledgedAt.UTC()})
	}
	var model *string
	if m := ex.Model(); m != "" {
		model = &m
	}
	runID, err := uuid.NewV7()
	if err != nil {
		return Run{}, err
	}
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		doc, err := q.LockDocument(ctx, dbq.LockDocumentParams{UserID: user, ID: docID})
		if err != nil {
			return err
		}
		if doc.Status == documents.StatusDeleted {
			return db.ErrNotFound
		}
		jobID, created, err := jobs.Enqueue(ctx, q, jobs.NewJob{Kind: Kind, Priority: jobs.PriorityHigh, MaxAttempts: maxAttempts,
			DedupeKey: Kind + ":" + docID.String(), Payload: payload{RunID: runID}})
		if err != nil || !created {
			return cmpErr(err, ErrRunning)
		}
		if err := q.AbandonExtractionRuns(ctx, docID); err != nil {
			return err
		}
		if err := q.InsertExtractionRun(ctx, dbq.InsertExtractionRunParams{ID: runID, DocumentID: docID, UserID: user, JobID: &jobID,
			Provider: provider, Model: model, External: ex.External(), Consent: consentJSON,
			SchemaVersion: documents.ExtractionSchema, PromptVersion: documents.PromptVersion, CreatedBy: actor}); err != nil {
			return err
		}
		if err := q.SetDocumentStatus(ctx, dbq.SetDocumentStatusParams{Status: documents.StatusExtracting, ID: docID}); err != nil {
			return err
		}
		detail := map[string]any{"run_id": runID.String(), "provider": provider, "model": ex.Model(), "external": ex.External()}
		if consent != nil && ex.External() {
			detail["consent_acknowledged_at"] = consent.AcknowledgedAt.UTC()
		}
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "document.extract",
			TargetType: "document", TargetID: docID.String(), Detail: detail})
	})
	if err != nil {
		return Run{}, err
	}
	runs, err := s.List(ctx, user, docID)
	for _, r := range runs {
		if r.ID == runID {
			return r, nil
		}
	}
	return Run{}, cmpErr(err, db.ErrNotFound)
}

// cmpErr returns err, or otherwise fallback.
func cmpErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// List returns the runs of one of the user's documents, newest first; db.ErrNotFound for an
// unknown document. A deleted document has no runs left.
func (s *Service) List(ctx context.Context, user, docID uuid.UUID) ([]Run, error) {
	q := s.db.Q()
	if _, err := q.GetDocument(ctx, dbq.GetDocumentParams{UserID: user, ID: docID}); err != nil {
		return nil, db.MapErr(err)
	}
	rows, err := q.ListExtractionRuns(ctx, dbq.ListExtractionRunsParams{UserID: user, DocumentID: docID})
	if err != nil {
		return nil, err
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = Run{ID: r.ID, DocumentID: r.DocumentID, Status: r.Status, Provider: r.Provider, Model: r.Model, External: r.External,
			SchemaVersion: r.SchemaVersion, PromptVersion: r.PromptVersion, ProviderRequestID: r.ProviderRequestID, DocMeta: r.DocMeta,
			Usage: r.Usage, Warnings: r.Warnings, ErrorClass: r.ErrorClass, RowCount: int(r.RowCount), CreatedBy: r.CreatedBy,
			CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
		if r.Consent != nil {
			out[i].Consent = &Consent{}
			if err := json.Unmarshal(r.Consent, out[i].Consent); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// errGone means the document or the run was deleted while the job ran: nothing to record.
var errGone = errors.New("extract: document deleted")

// Handle is the Kind job handler. Transient provider errors retry (Retry-After reschedules);
// anything else fails the run with its class, keeping the document and any raw response.
func (s *Service) Handle(ctx context.Context, j jobs.Job) error {
	var p payload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return jobs.Permanent(err)
	}
	run, err := s.db.Q().GetExtractionRun(ctx, p.RunID)
	if errors.Is(db.MapErr(err), db.ErrNotFound) {
		return nil // deleted with its document
	}
	if err != nil {
		return err
	}
	if run.Status != "queued" && run.Status != "running" {
		return nil // already finished (a retry after a lost lease)
	}
	if n, err := s.db.Q().StartExtractionRun(ctx, run.ID); err != nil || n == 0 {
		return err
	}
	log := s.log.With("run_id", run.ID, "provider", run.Provider)

	ex := s.providers[run.Provider]
	if ex == nil {
		return s.fail(ctx, log, run, nil, errorf(ClassProviderUnavailable, "%s is no longer configured", run.Provider))
	}
	if ex.External() {
		on, err := Enabled(ctx, s.db.Q(), run.UserID, run.Provider)
		if err != nil {
			return err
		}
		var c Consent
		_ = json.Unmarshal(run.Consent, &c)
		switch {
		case !on:
			return s.fail(ctx, log, run, nil, errorf(ClassProviderDisabled, "%s was disabled after the run was queued", run.Provider))
		case c.Provider != ex.ID() || c.Model != ex.Model():
			return s.fail(ctx, log, run, nil, errorf(ClassConsentMismatch, "the configured model changed after consent"))
		}
	}
	doc, err := s.docs.Get(ctx, run.UserID, run.DocumentID)
	if err != nil {
		return gone(err)
	}
	pdf, err := s.docs.File(ctx, run.UserID, run.DocumentID)
	if err != nil {
		return gone(err)
	}

	resp, err := ex.Extract(ctx, Request{PDF: pdf, Pages: doc.Pages, Prompt: prompts.LabExtractionV1, Schema: schemas.LabExtractionV1})
	clear(pdf)
	if err == nil {
		err = s.finish(ctx, run, &resp, nil)
		if errors.Is(err, errGone) {
			return nil
		}
		if err == nil {
			log.Info("extraction succeeded", "rows", len(resp.Extraction.Rows))
		}
		return err
	}
	var e *Error
	if !errors.As(err, &e) {
		return err // context ended or an internal error: the job retries
	}
	if e.Retryable() && j.Attempt < j.MaxAttempts {
		if err := s.db.Q().RequeueExtractionRun(ctx, dbq.RequeueExtractionRunParams{ErrorClass: &e.Class, ID: run.ID}); err != nil {
			return err
		}
		if e.RetryAfter > 0 && e.RetryAfter <= maxRetryAfter {
			return jobs.RescheduleAt(s.now().Add(e.RetryAfter), e)
		}
		return e
	}
	return s.fail(ctx, log, run, &resp, e)
}

func gone(err error) error {
	if errors.Is(err, db.ErrNotFound) || errors.Is(err, documents.ErrShredded) {
		return nil
	}
	return err
}

// fail records a failed run and returns e as a permanent job error.
func (s *Service) fail(ctx context.Context, log *slog.Logger, run dbq.ExtractionRun, resp *Response, e *Error) error {
	if err := s.finish(ctx, run, resp, e); err != nil {
		return gone(err)
	}
	log.Warn("extraction failed", "error_class", e.Class)
	return jobs.Permanent(e)
}

// finish stores the outcome under the document lock: the sealed raw response (also for an
// invalid one), the rows of a successful run, and the document status.
func (s *Service) finish(ctx context.Context, run dbq.ExtractionRun, resp *Response, failure *Error) error {
	return s.db.Tx(ctx, func(q *dbq.Queries) error {
		doc, err := q.LockDocument(ctx, dbq.LockDocumentParams{UserID: run.UserID, ID: run.DocumentID})
		if errors.Is(db.MapErr(err), db.ErrNotFound) || (err == nil && doc.Status == documents.StatusDeleted) {
			return errGone
		}
		if err != nil {
			return err
		}
		cur, err := q.GetExtractionRun(ctx, run.ID)
		if errors.Is(db.MapErr(err), db.ErrNotFound) {
			return errGone
		}
		if err != nil || (cur.Status != "queued" && cur.Status != "running") {
			return err
		}
		params := dbq.FinishExtractionRunParams{ID: run.ID, Status: "succeeded", DocMeta: json.RawMessage("{}"), Usage: json.RawMessage("{}"), Warnings: []string{}}
		if failure != nil {
			params.Status, params.ErrorClass = "failed", &failure.Class
		}
		if resp != nil {
			if resp.ModelID != "" {
				params.Model = &resp.ModelID
			}
			if resp.RequestID != "" {
				params.ProviderRequestID = &resp.RequestID
			}
			if params.Usage, err = json.Marshal(resp.Usage); err != nil {
				return err
			}
			if len(resp.Raw) > 0 {
				if params.ResponseBlobSha256, err = s.storeRawResponse(ctx, q, run, resp.Raw); err != nil {
					return err
				}
			}
		}
		if failure == nil {
			x := resp.Extraction
			if params.DocMeta, err = json.Marshal(x.Document); err != nil {
				return err
			}
			params.Warnings = x.Warnings
			if err := insertRows(ctx, q, run.ID, x.Rows); err != nil {
				return err
			}
		}
		if err := q.FinishExtractionRun(ctx, params); err != nil {
			return err
		}
		status := documents.StatusNeedsReview
		if failure != nil {
			ok, err := q.HasSucceededExtraction(ctx, run.DocumentID)
			if err != nil {
				return err
			}
			if !ok {
				status = documents.StatusUploaded
			}
		}
		return q.SetDocumentStatus(ctx, dbq.SetDocumentStatusParams{Status: status, ID: run.DocumentID})
	})
}

// storeRawResponse seals the raw provider response under the document's key and keeps it in
// the blob store. It returns the blob hash, or errGone when the document was shredded meanwhile.
func (s *Service) storeRawResponse(ctx context.Context, q *dbq.Queries, run dbq.ExtractionRun, raw []byte) ([]byte, error) {
	sealed, err := s.docs.Seal(ctx, q, run.DocumentID, "extraction_response:"+run.ID.String(), raw)
	if errors.Is(err, documents.ErrShredded) {
		return nil, errGone
	}
	if err != nil {
		return nil, err
	}
	info, err := s.blobs.Put(ctx, q, bytes.NewReader(sealed), blob.Plain)
	if err != nil {
		return nil, err
	}
	if err := blob.Retain(ctx, q, info.SHA256); err != nil {
		return nil, err
	}
	return info.SHA256, nil
}

func insertRows(ctx context.Context, q *dbq.Queries, runID uuid.UUID, rows []documents.Row) error {
	for _, r := range rows {
		page := int32(r.Page)                                                                                                   //nolint:gosec // validated: 1..MaxExtractionPages
		conf := float32(r.Confidence)                                                                                           // validated: 0..1
		p := dbq.InsertExtractedRowParams{RunID: runID, RowIndex: int32(r.RowIndex), Page: &page, AnalyteLabel: r.AnalyteLabel, //nolint:gosec // validated: < MaxExtractionRows
			ValueText: r.ValueText, ValueNumeric: r.ValueNumeric, Comparator: r.Comparator, UnitText: r.UnitText,
			ReferenceRangeText: r.ReferenceRangeText, RefLow: r.RefLow, RefHigh: r.RefHigh, AbnormalFlagPrinted: r.PrintedFlag,
			SpecimenType: r.SpecimenType, CollectedAt: r.CollectedAt, ReportedAt: r.ReportedAt, Laboratory: r.Laboratory,
			EvidenceText: &r.EvidenceText, Confidence: &conf, Warnings: r.Warnings}
		if r.BBox != nil {
			b, err := json.Marshal(r.BBox)
			if err != nil {
				return err
			}
			p.Bbox = b
		}
		if err := q.InsertExtractedRow(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// Setup builds the Service with the extractors cfg configures and registers the job. Without
// a blob store or master key it returns nil, like documents.Register.
func Setup(runner *jobs.Runner, d *db.DB, blobs *blob.Store, keys *crypto.Keyring, log *slog.Logger, cfg config.Config) (*Service, error) {
	if blobs == nil || keys == nil {
		return nil, nil
	}
	s, err := New(d, blobs, keys, log, Configured(cfg)...)
	if err != nil {
		return nil, err
	}
	runner.Register(Kind, s.Handle)
	return s, nil
}
