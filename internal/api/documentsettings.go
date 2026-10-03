package api

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/documents/extract"
)

// Lab document settings (lab-documents.md#privacy-controls) inside GET and PATCH
// /api/v1/settings: external extractor enablement (extract.SetEnabled) and the retention
// policy of originals (documents.SetPolicy). Both services audit their changes.

// maxDocumentRetentionDays is the bound documents.SetPolicy enforces (100 years).
const maxDocumentRetentionDays = 36500

// externalAI maps each external provider to its settings field.
func externalAI(s *oapi.Settings) map[string]**bool {
	return map[string]**bool{
		extract.Gemini:           &s.DocumentsExternalAiGeminiEnabled,
		extract.OpenAI:           &s.DocumentsExternalAiOpenaiEnabled,
		extract.OpenAICompatible: &s.DocumentsExternalAiOpenaiCompatibleEnabled,
	}
}

func documentSettings(ctx context.Context, q *dbq.Queries, user uuid.UUID, out *oapi.Settings) error {
	for provider, field := range externalAI(out) {
		on, err := extract.Enabled(ctx, q, user, provider)
		if err != nil {
			return err
		}
		*field = &on
	}
	p, err := documents.GetPolicy(ctx, q, user)
	if err != nil {
		return err
	}
	if out.DocumentsRetentionDays, err = json.Marshal(p.RetentionDays); err != nil {
		return err
	}
	out.DocumentsDeleteOriginalAfterConfirmation = &p.DeleteOriginalAfterConfirmation
	return nil
}

// updateDocumentSettings applies the document keys present in body. documents.retention_days
// null keeps originals.
func (o *owner) updateDocumentSettings(ctx context.Context, body *oapi.Settings) error {
	p := auth.PrincipalFrom(ctx)
	if body.DocumentsRetentionDays != nil || body.DocumentsDeleteOriginalAfterConfirmation != nil {
		policy, err := documents.GetPolicy(ctx, o.opts.DB.Q(), p.UserID)
		if err != nil {
			return err
		}
		if body.DocumentsRetentionDays != nil {
			var days *int
			if json.Unmarshal(body.DocumentsRetentionDays, &days) != nil || (days != nil && (*days < 1 || *days > maxDocumentRetentionDays)) {
				return problemErr(CodeValidationFailed, "invalid retention period", FieldError{Pointer: "/documents.retention_days",
					Detail: "must be a whole number of days from 1 to 36500, or null to keep originals"})
			}
			policy.RetentionDays = days
		}
		if v := body.DocumentsDeleteOriginalAfterConfirmation; v != nil {
			policy.DeleteOriginalAfterConfirmation = *v
		}
		if err := documents.SetPolicy(ctx, o.opts.DB, p.UserID, p.Actor(), policy); err != nil {
			return err
		}
	}
	for provider, field := range externalAI(body) {
		if *field != nil {
			if err := extract.SetEnabled(ctx, o.opts.DB, p.UserID, p.Actor(), provider, **field); err != nil {
				return err
			}
		}
	}
	return nil
}
