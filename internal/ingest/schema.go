package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Wire schemas (schemas/*.json). The Go checks below mirror the JSON Schema files; the
// schema test keeps the two in agreement.
const (
	BatchSchema     = "vitamux.ingest.batch/1"
	HeartbeatSchema = "vitamux.ingest.heartbeat/1"

	MaxItems           = 1000
	MaxStreams         = 64
	MaxCheckpointBytes = 16 << 10
)

var (
	connectionIDRe = regexp.MustCompile(`^conn_[0-9a-f]{32}$`)
	clientNameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	clientVerRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,31}$`)
	streamRe       = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`)
	controlRe      = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	contentTypeRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*(;.*)?$`)
	jsonTypeRe     = regexp.MustCompile(`^application/([a-z0-9.-]+\+)?json(;.*)?$`)
	sha256Re       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	migrationRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)
	errorClassRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

var clientKinds = map[string]bool{"device": true, "importer": true, "collector": true}

// Batch is the body of POST /api/ingest/v1/batches (schemas/ingest-batch.v1.json).
type Batch struct {
	Schema       string      `json:"schema"`
	ConnectionID string      `json:"connection_id"`
	Client       Client      `json:"client"`
	Items        []Item      `json:"items"`
	Provenance   *Provenance `json:"provenance,omitempty"`
}

// Client identifies the uploading program.
type Client struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Provenance marks batches written by migration importers.
type Provenance struct {
	MigrationSource *string `json:"migration_source,omitempty"`
}

// Item is one raw source record: inline JSON Body, or a BlobSHA256 uploaded separately.
type Item struct {
	Stream      string          `json:"stream"`
	ExternalKey string          `json:"external_key"`
	FetchedAt   string          `json:"fetched_at"`
	ContentType string          `json:"content_type"`
	SHA256      string          `json:"sha256,omitempty"`
	Request     *ItemRequest    `json:"request,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"` // exact bytes as sent
	BlobSHA256  string          `json:"blob_sha256,omitempty"`
}

// ItemRequest is the client's description of how it fetched the item.
type ItemRequest struct {
	Endpoint string          `json:"endpoint,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
}

// Heartbeat is the body of POST /api/ingest/v1/heartbeat (schemas/heartbeat.v1.json).
type Heartbeat struct {
	Schema             string         `json:"schema"`
	ConnectionID       string         `json:"connection_id"`
	Client             Client         `json:"client"`
	SentAt             string         `json:"sent_at"`
	PendingFailedUnits int            `json:"pending_failed_units,omitempty"`
	LastErrorClass     *string        `json:"last_error_class,omitempty"`
	LastErrorAt        *string        `json:"last_error_at,omitempty"`
	Streams            []StreamStatus `json:"streams,omitempty"`
}

// StreamStatus is the client's view of one stream.
type StreamStatus struct {
	Stream             string          `json:"stream"`
	Checkpoint         json.RawMessage `json:"checkpoint,omitempty"`
	LastSuccessAt      *string         `json:"last_success_at,omitempty"`
	PendingFailedUnits int             `json:"pending_failed_units,omitempty"`
	LastErrorClass     *string         `json:"last_error_class,omitempty"`
}

// FieldError points at one invalid input (RFC 6901 pointer). Detail never echoes values.
type FieldError struct {
	Pointer string
	Detail  string
}

// ValidationError lists every problem found in a request body.
type ValidationError struct{ Errors []FieldError }

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, f := range e.Errors {
		parts[i] = f.Pointer + ": " + f.Detail
	}
	return "ingest: invalid body: " + strings.Join(parts, "; ")
}

// DecodeBatch parses and validates a batch body. Errors are *ValidationError.
func DecodeBatch(data []byte) (*Batch, error) {
	var b Batch
	if err := decodeStrict(data, &b); err != nil {
		return nil, err
	}
	return &b, b.Validate()
}

// DecodeHeartbeat parses and validates a heartbeat body. Errors are *ValidationError.
func DecodeHeartbeat(data []byte) (*Heartbeat, error) {
	var h Heartbeat
	if err := decodeStrict(data, &h); err != nil {
		return nil, err
	}
	return &h, h.Validate()
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("trailing data after the JSON value")
		}
	}
	if err == nil {
		return nil
	}
	ptr, detail := "", "invalid JSON"
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.As(err, &typeErr):
		ptr, detail = "/"+strings.ReplaceAll(typeErr.Field, ".", "/"), "must be "+typeErr.Type.Kind().String()
	case errors.As(err, &syntaxErr):
		detail = "invalid JSON at offset " + strconv.FormatInt(syntaxErr.Offset, 10)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		detail = strings.TrimPrefix(err.Error(), "json: ")
	case strings.HasPrefix(err.Error(), "trailing"):
		detail = err.Error()
	}
	return &ValidationError{Errors: []FieldError{{Pointer: ptr, Detail: detail}}}
}

type checker struct{ errs []FieldError }

func (c *checker) check(ok bool, ptr, detail string) {
	if !ok {
		c.errs = append(c.errs, FieldError{Pointer: ptr, Detail: detail})
	}
}

func (c *checker) err() error {
	if len(c.errs) == 0 {
		return nil
	}
	return &ValidationError{Errors: c.errs}
}

func (c *checker) client(cl Client) {
	c.check(clientKinds[cl.Kind], "/client/kind", "must be device, importer or collector")
	c.check(clientNameRe.MatchString(cl.Name), "/client/name", "must be 1-64 letters, digits, '.', '_' or '-'")
	c.check(clientVerRe.MatchString(cl.Version), "/client/version", "must be 1-32 letters, digits, '.', '+', '_' or '-'")
}

func (c *checker) timestamp(s, ptr string) {
	_, err := time.Parse(time.RFC3339Nano, s)
	c.check(err == nil, ptr, "must be an RFC 3339 date-time with offset")
}

func (c *checker) stream(s, ptr string) {
	c.check(len(s) <= 64 && streamRe.MatchString(s), ptr, "must be a dotted lowercase stream name of at most 64 characters")
}

// Validate checks b against schemas/ingest-batch.v1.json, plus the item checksums.
func (b *Batch) Validate() error {
	var c checker
	c.check(b.Schema == BatchSchema, "/schema", "must be "+BatchSchema)
	c.check(connectionIDRe.MatchString(b.ConnectionID), "/connection_id", "must be conn_ followed by 32 lowercase hex characters")
	c.client(b.Client)
	c.check(len(b.Items) >= 1 && len(b.Items) <= MaxItems, "/items", fmt.Sprintf("must hold 1 to %d items", MaxItems))
	for i, it := range b.Items {
		it.validate(&c, "/items/"+strconv.Itoa(i))
	}
	if b.Provenance != nil && b.Provenance.MigrationSource != nil {
		c.check(migrationRe.MatchString(*b.Provenance.MigrationSource), "/provenance/migration_source", "must be a lowercase source name")
	}
	return c.err()
}

func (it Item) validate(c *checker, p string) {
	c.stream(it.Stream, p+"/stream")
	c.check(it.ExternalKey != "" && utf8.RuneCountInString(it.ExternalKey) <= 512 && utf8.ValidString(it.ExternalKey) && !controlRe.MatchString(it.ExternalKey),
		p+"/external_key", "must be 1-512 characters without control characters")
	c.timestamp(it.FetchedAt, p+"/fetched_at")
	c.check(utf8.RuneCountInString(it.ContentType) <= 128 && contentTypeRe.MatchString(it.ContentType), p+"/content_type", "must be a lowercase media type")
	c.check(it.SHA256 == "" || sha256Re.MatchString(it.SHA256), p+"/sha256", "must be 64 lowercase hex characters")
	if it.Request != nil {
		c.check(utf8.RuneCountInString(it.Request.Endpoint) <= 2048, p+"/request/endpoint", "must be at most 2048 characters")
		c.check(it.Request.Params == nil || isObject(it.Request.Params), p+"/request/params", "must be an object")
	}
	hasBody, hasBlob := it.Body != nil, it.BlobSHA256 != ""
	c.check(hasBody != hasBlob, p, "must have exactly one of body or blob_sha256")
	switch {
	case hasBody:
		c.check(isObject(it.Body) || isArray(it.Body), p+"/body", "must be an object or array")
		c.check(jsonTypeRe.MatchString(it.ContentType), p+"/content_type", "must be a JSON media type when body is inline")
		if sha256Re.MatchString(it.SHA256) {
			sum := sha256.Sum256(it.Body)
			c.check(hex.EncodeToString(sum[:]) == it.SHA256, p+"/sha256", "does not match the body bytes")
		}
	case hasBlob:
		c.check(sha256Re.MatchString(it.BlobSHA256), p+"/blob_sha256", "must be 64 lowercase hex characters")
		c.check(it.SHA256 == "" || it.SHA256 == it.BlobSHA256, p+"/sha256", "must equal blob_sha256")
	}
}

// Validate checks h against schemas/heartbeat.v1.json.
func (h *Heartbeat) Validate() error {
	var c checker
	c.check(h.Schema == HeartbeatSchema, "/schema", "must be "+HeartbeatSchema)
	c.check(connectionIDRe.MatchString(h.ConnectionID), "/connection_id", "must be conn_ followed by 32 lowercase hex characters")
	c.client(h.Client)
	c.timestamp(h.SentAt, "/sent_at")
	c.check(h.PendingFailedUnits >= 0, "/pending_failed_units", "must not be negative")
	c.check(h.LastErrorClass == nil || errorClassRe.MatchString(*h.LastErrorClass), "/last_error_class", "must be a lowercase error class")
	if h.LastErrorAt != nil {
		c.timestamp(*h.LastErrorAt, "/last_error_at")
	}
	c.check(len(h.Streams) <= MaxStreams, "/streams", fmt.Sprintf("must hold at most %d streams", MaxStreams))
	for i, s := range h.Streams {
		p := "/streams/" + strconv.Itoa(i)
		c.stream(s.Stream, p+"/stream")
		c.check(s.Checkpoint == nil || isObject(s.Checkpoint), p+"/checkpoint", "must be an object")
		c.check(len(s.Checkpoint) <= MaxCheckpointBytes, p+"/checkpoint", "must be at most 16 KiB")
		if s.LastSuccessAt != nil {
			c.timestamp(*s.LastSuccessAt, p+"/last_success_at")
		}
		c.check(s.PendingFailedUnits >= 0, p+"/pending_failed_units", "must not be negative")
		c.check(s.LastErrorClass == nil || errorClassRe.MatchString(*s.LastErrorClass), p+"/last_error_class", "must be a lowercase error class")
	}
	return c.err()
}

func isObject(raw json.RawMessage) bool { return firstByte(raw) == '{' }
func isArray(raw json.RawMessage) bool  { return firstByte(raw) == '[' }

func firstByte(raw json.RawMessage) byte {
	t := bytes.TrimLeft(raw, " \t\r\n")
	if len(t) == 0 {
		return 0
	}
	return t[0]
}

// ParseConnectionID returns the UUID of a conn_<32 hex> identifier.
func ParseConnectionID(s string) (uuid.UUID, error) {
	if !connectionIDRe.MatchString(s) {
		return uuid.Nil, errors.New("ingest: malformed connection id")
	}
	return uuid.Parse(strings.TrimPrefix(s, "conn_"))
}

// FormatConnectionID returns the conn_<32 hex> form of a connection UUID.
func FormatConnectionID(id uuid.UUID) string { return "conn_" + hex.EncodeToString(id[:]) }

// Raw converts a validated item for StoreRaw. Call it only after Validate succeeded.
func (it Item) Raw() (RawItem, error) {
	fetched, err := time.Parse(time.RFC3339Nano, it.FetchedAt)
	if err != nil {
		return RawItem{}, err
	}
	r := RawItem{Stream: it.Stream, ExternalKey: it.ExternalKey, ContentType: it.ContentType, FetchedAt: fetched, Body: it.Body}
	if it.BlobSHA256 != "" {
		if r.BlobSHA256, err = hex.DecodeString(it.BlobSHA256); err != nil {
			return RawItem{}, err
		}
	}
	if it.Request != nil {
		r.Request.Endpoint = it.Request.Endpoint
		if it.Request.Params != nil {
			dec := json.NewDecoder(bytes.NewReader(it.Request.Params))
			dec.UseNumber()
			if err := dec.Decode(&r.Request.Params); err != nil {
				return RawItem{}, err
			}
		}
	}
	return r, nil
}
