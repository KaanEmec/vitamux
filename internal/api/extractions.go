package api

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/documents/extract"
)

// Extraction runs (J12.3, ADR-0013). Starting one checks provider enablement and consent
// server-side; listing never returns the raw provider response.
func (rt *router) extractionRoutes() {
	rt.handle("GET /api/v1/documents/{id}/extractions", scope(auth.ReadHealth), rt.ops.ListExtractions)
	rt.handle("POST /api/v1/documents/{id}/extractions", scope(auth.WriteDocuments), rt.ops.CreateExtraction)
}

func (o *owner) extractor() (*extract.Service, error) {
	if o.opts.Extract == nil {
		return nil, problemErr(CodeUnavailable, "extraction needs the database, the data directory and the master key")
	}
	return o.opts.Extract, nil
}

func formatExtractionID(id uuid.UUID) string { return "ext_" + hex.EncodeToString(id[:]) }

func apiExtraction(r extract.Run) oapi.Extraction {
	out := oapi.Extraction{ID: formatExtractionID(r.ID), DocumentID: formatDocumentID(r.DocumentID), Status: oapi.ExtractionStatus(r.Status),
		Provider: r.Provider, Model: r.Model, External: r.External, SchemaVersion: r.SchemaVersion, PromptVersion: r.PromptVersion,
		ProviderRequestID: r.ProviderRequestID, Document: r.DocMeta, Usage: r.Usage, Warnings: r.Warnings, ErrorClass: r.ErrorClass,
		RowCount: r.RowCount, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
	if r.Consent != nil {
		out.Consent = &oapi.ExtractionConsent{Provider: r.Consent.Provider, Model: r.Consent.Model, AcknowledgedAt: r.Consent.AcknowledgedAt}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	return out
}

// CreateExtraction queues an extraction. Idempotency-Key is accepted; a repeated request while
// the first run is queued or running answers 409 conflict.
func (o *owner) CreateExtraction(ctx context.Context, req oapi.CreateExtractionRequestObject) (oapi.CreateExtractionResponseObject, error) {
	svc, err := o.extractor()
	if err != nil {
		return nil, err
	}
	id, err := parseDocumentID(req.ID)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, problemErr(CodeValidationFailed, "provider is required", FieldError{Pointer: "/provider", Detail: "required"})
	}
	provider := string(req.Body.Provider)
	var consent *extract.Consent
	if c := req.Body.Consent; c != nil {
		consent = &extract.Consent{Provider: c.Provider, Model: c.Model, AcknowledgedAt: c.AcknowledgedAt}
	}
	p := auth.PrincipalFrom(ctx)
	run, err := svc.Start(ctx, p.UserID, id, p.Actor(), provider, consent)
	if err != nil {
		return nil, startError(svc, provider, err)
	}
	return oapi.CreateExtraction202JSONResponse(apiExtraction(run)), nil
}

// startError maps refusals: unknown provider 422, not configured or disabled 403, missing or
// mismatched consent 409 consent_required, a run in progress 409 conflict.
func startError(svc *extract.Service, provider string, err error) error {
	switch {
	case errors.Is(err, extract.ErrUnknownProvider):
		return problemErr(CodeValidationFailed, "unknown provider", FieldError{Pointer: "/provider", Detail: "must be fake, gemini, openai or openai_compatible"})
	case errors.Is(err, extract.ErrNotConfigured):
		return problemErr(CodeForbidden, provider+" is not configured on this server")
	case errors.Is(err, extract.ErrDisabled):
		return problemErr(CodeForbidden, provider+" is disabled; enable it in settings ("+extract.SettingEnabled(provider)+")")
	case errors.Is(err, extract.ErrConsentRequired):
		return problemErr(CodeConsentRequired, "sending the PDF to "+provider+" needs consent {provider, model, acknowledged_at}")
	case errors.Is(err, extract.ErrConsentMismatch):
		return problemErr(CodeConsentRequired, "consent must name provider "+provider+" and model "+svc.Provider(provider).Model())
	case errors.Is(err, extract.ErrRunning):
		return problemErr(CodeConflict, "an extraction of this document is already queued or running")
	}
	return err
}

func (o *owner) ListExtractions(ctx context.Context, req oapi.ListExtractionsRequestObject) (oapi.ListExtractionsResponseObject, error) {
	svc, err := o.extractor()
	if err != nil {
		return nil, err
	}
	id, err := parseDocumentID(req.ID)
	if err != nil {
		return nil, err
	}
	runs, err := svc.List(ctx, auth.PrincipalFrom(ctx).UserID, id)
	if err != nil {
		return nil, err
	}
	out := oapi.ListExtractions200JSONResponse{Extractions: make([]oapi.Extraction, len(runs))}
	for i, r := range runs {
		out.Extractions[i] = apiExtraction(r)
	}
	return out, nil
}
