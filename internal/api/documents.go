package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/documents"
)

// Lab document storage (J12.1; docs/architecture/lab-documents.md#storage). Every query is
// scoped to the caller's user.
func (rt *router) documentRoutes() {
	read, write := scope(auth.ReadHealth), scope(auth.WriteDocuments)
	rt.handle("GET /api/v1/documents", read, rt.ops.ListDocuments)
	rt.handle("POST /api/v1/documents", write, rt.ops.UploadDocument)
	rt.handle("GET /api/v1/documents/{id}", read, rt.ops.GetDocument)
	rt.handle("DELETE /api/v1/documents/{id}", write, rt.ops.DeleteDocument)
	rt.handle("GET /api/v1/documents/{id}/file", read, rt.ops.GetDocumentFile)
}

// docs returns the document store; it needs the database, the blob store and the master key.
func (o *owner) docs() (*documents.Store, error) {
	if o.opts.DB == nil || o.opts.Blobs == nil || o.opts.Keys == nil {
		return nil, problemErr(CodeUnavailable, "document storage needs the database, the data directory and the master key")
	}
	return documents.New(o.opts.DB, o.opts.Blobs, o.opts.Keys), nil
}

var documentIDRe = regexp.MustCompile(`^doc_[0-9a-f]{32}$`)

func formatDocumentID(id uuid.UUID) string { return "doc_" + hex.EncodeToString(id[:]) }

// parseDocumentID accepts doc_<32 hex>; anything else is not found.
func parseDocumentID(s string) (uuid.UUID, error) {
	if !documentIDRe.MatchString(s) {
		return uuid.Nil, db.ErrNotFound
	}
	return uuid.Parse(strings.TrimPrefix(s, "doc_"))
}

func apiDocument(d documents.Document) oapi.Document {
	out := oapi.Document{ID: formatDocumentID(d.ID), Status: oapi.DocumentStatus(d.Status), SizeBytes: int(d.SizeBytes),
		PageCount: d.Pages, UploadedAt: d.UploadedAt, RetentionUntil: d.RetentionUntil, DeletedAt: d.DeletedAt}
	if d.SHA256 != nil {
		s := hex.EncodeToString(d.SHA256)
		out.Sha256 = &s
	}
	if d.Filename != "" {
		out.Filename = &d.Filename
	}
	return out
}

func (o *owner) UploadDocument(ctx context.Context, req oapi.UploadDocumentRequestObject) (oapi.UploadDocumentResponseObject, error) {
	store, err := o.docs()
	if err != nil {
		return nil, err
	}
	var body io.Reader
	var filename string
	switch {
	case req.Body != nil:
		body = req.Body
	case req.MultipartBody != nil:
		if body, filename, err = filePart(req.MultipartBody); err != nil {
			return nil, err
		}
	default:
		return nil, problemErr(CodeValidationFailed, "send the PDF as multipart/form-data (part file) or as an application/pdf body")
	}
	p := auth.PrincipalFrom(ctx)
	doc, existing, err := store.Upload(ctx, p.UserID, p.Actor(), filename, body)
	if err != nil {
		return nil, uploadError(err)
	}
	if existing {
		return oapi.UploadDocument200JSONResponse(apiDocument(doc)), nil
	}
	return oapi.UploadDocument201JSONResponse(apiDocument(doc)), nil
}

// maxFilename bounds the stored original filename, in bytes.
const maxFilename = 255

// filePart returns the "file" part of a multipart upload and its filename, skipping other
// parts. The part streams the content; the store reads it.
func filePart(mr *multipart.Reader) (io.Reader, string, error) {
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, "", problemErr(CodeValidationFailed, "the form has no file part", FieldError{Pointer: "/file", Detail: "required"})
		}
		if err != nil {
			return nil, "", uploadError(err)
		}
		if part.FormName() != "file" {
			continue
		}
		name := part.FileName()
		if len(name) > maxFilename || !utf8.ValidString(name) {
			return nil, "", problemErr(CodeValidationFailed, "the filename is too long or not UTF-8", FieldError{Pointer: "/file", Detail: "invalid filename"})
		}
		return part, name, nil
	}
}

// uploadError maps upload failures to problems: the reject reason for refused files, 413 for
// an oversized body.
func uploadError(err error) error {
	var re *documents.RejectError
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &re) && re.Reason == documents.ReasonTooLarge, errors.As(err, &tooLarge):
		return problemErr(CodePayloadTooLarge, "the PDF exceeds the upload limit")
	case errors.As(err, &re):
		return problemErr(CodeValidationFailed, re.Detail, FieldError{Pointer: "/file", Detail: string(re.Reason)})
	}
	return err
}

func (o *owner) ListDocuments(ctx context.Context, req oapi.ListDocumentsRequestObject) (oapi.ListDocumentsResponseObject, error) {
	store, err := o.docs()
	if err != nil {
		return nil, err
	}
	p, err := o.newPage("documents", struct{}{}, req.Params.Limit, req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	var after *uuid.UUID
	if p.after != nil {
		id, err := p.afterUUID()
		if err != nil {
			return nil, err
		}
		after = &id
	}
	docs, err := store.List(ctx, auth.PrincipalFrom(ctx).UserID, after, p.lim())
	if err != nil {
		return nil, err
	}
	docs, more, next := trim(o, p, docs, func(d documents.Document) (time.Time, string) { return d.UploadedAt, d.ID.String() })
	out := oapi.ListDocuments200JSONResponse{Documents: make([]oapi.Document, len(docs)), HasMore: more, NextCursor: next}
	for i, d := range docs {
		out.Documents[i] = apiDocument(d)
	}
	return out, nil
}

func (o *owner) GetDocument(ctx context.Context, req oapi.GetDocumentRequestObject) (oapi.GetDocumentResponseObject, error) {
	store, err := o.docs()
	if err != nil {
		return nil, err
	}
	id, err := parseDocumentID(req.ID)
	if err != nil {
		return nil, err
	}
	d, err := store.Get(ctx, auth.PrincipalFrom(ctx).UserID, id)
	if err != nil {
		return nil, err
	}
	return oapi.GetDocument200JSONResponse(apiDocument(d)), nil
}

func (o *owner) GetDocumentFile(ctx context.Context, req oapi.GetDocumentFileRequestObject) (oapi.GetDocumentFileResponseObject, error) {
	store, err := o.docs()
	if err != nil {
		return nil, err
	}
	id, err := parseDocumentID(req.ID)
	if err != nil {
		return nil, err
	}
	pdf, err := store.File(ctx, auth.PrincipalFrom(ctx).UserID, id)
	if err != nil {
		return nil, err
	}
	return oapi.GetDocumentFile200ApplicationPdfResponse{Body: bytes.NewReader(pdf), ContentLength: int64(len(pdf))}, nil
}

func (o *owner) DeleteDocument(ctx context.Context, req oapi.DeleteDocumentRequestObject) (oapi.DeleteDocumentResponseObject, error) {
	store, err := o.docs()
	if err != nil {
		return nil, err
	}
	id, err := parseDocumentID(req.ID)
	if err != nil {
		return nil, err
	}
	derived := documents.Derived(req.Params.Derived)
	if derived != documents.KeepDerived && derived != documents.DeleteDerived {
		return nil, problemErr(CodeValidationFailed, "derived must be keep or delete", FieldError{Pointer: "/derived", Detail: "must be keep or delete"})
	}
	p := auth.PrincipalFrom(ctx)
	if _, err := store.Delete(ctx, p.UserID, id, derived, p.Actor()); err != nil {
		return nil, err
	}
	return oapi.DeleteDocument204Response{}, nil
}
