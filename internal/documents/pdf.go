package documents

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// Limits bound an upload (lab-documents.md#storage).
type Limits struct {
	MaxBytes int64
	MaxPages int
}

// DefaultLimits are the documented defaults: 20 MiB and 50 pages.
var DefaultLimits = Limits{MaxBytes: 20 << 20, MaxPages: 50}

// Reason is the stable code of a rejected upload; the API returns it to the caller.
type Reason string

const (
	ReasonNotPDF       Reason = "not_pdf"
	ReasonTooLarge     Reason = "too_large"
	ReasonTooManyPages Reason = "too_many_pages"
	ReasonEncrypted    Reason = "encrypted"
	ReasonMalformed    Reason = "malformed"
	ReasonEmpty        Reason = "empty"
)

// RejectError says why an upload was refused. Its text never contains document content.
type RejectError struct {
	Reason Reason
	Detail string
}

func (e *RejectError) Error() string {
	return "documents: rejected (" + string(e.Reason) + "): " + e.Detail
}

func reject(r Reason, format string, args ...any) error {
	return &RejectError{Reason: r, Detail: fmt.Sprintf(format, args...)}
}

// maxInflated bounds the bytes Validate decompresses from object streams (zip-bomb guard).
const maxInflated = 64 << 20

var (
	pageObj   = regexp.MustCompile(`/Type\s*/Page(?:[^A-Za-z0-9]|$)`)
	encrypt   = regexp.MustCompile(`/Encrypt(?:[^A-Za-z0-9]|$)`)
	streamRe  = regexp.MustCompile(`stream\r?\n`)
	objStmTag = regexp.MustCompile(`/Type\s*/ObjStm(?:[^A-Za-z0-9]|$)`)
)

// Validate checks that b is a plausible, unencrypted PDF within lim and returns its page
// count. It is a structure scan, not a parser, and never renders anything:
//
//   - the file must start with %PDF- and contain %%EOF;
//   - an /Encrypt entry anywhere (trailer or cross-reference stream) rejects the file;
//   - pages are counted as /Type /Page objects, in the file body and inside FlateDecode
//     object streams (PDF 1.5+), decompressed up to 64 MiB in total.
//
// Limitations (v1): a page object repeated by an incremental update is counted twice, so a
// file near the page limit may be refused; object streams with other filters are not
// inspected; a page tree that lives only in such streams is reported as malformed.
func Validate(b []byte, lim Limits) (pages int, err error) {
	if len(b) == 0 {
		return 0, reject(ReasonEmpty, "the file is empty")
	}
	if int64(len(b)) > lim.MaxBytes {
		return 0, reject(ReasonTooLarge, "the file exceeds %d MiB", lim.MaxBytes>>20)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		return 0, reject(ReasonNotPDF, "the file is not a PDF")
	}
	if !bytes.Contains(b, []byte("%%EOF")) {
		return 0, reject(ReasonMalformed, "the PDF is truncated (no %%EOF marker)")
	}
	if encrypt.Match(b) {
		return 0, reject(ReasonEncrypted, "encrypted PDFs are not accepted; remove the password and upload again")
	}
	pages = len(pageObj.FindAllIndex(b, -1))
	budget := int64(maxInflated)
	for _, loc := range streamRe.FindAllIndex(b, -1) {
		if bytes.HasSuffix(b[:loc[0]], []byte("end")) || !isObjStm(b, loc[0]) {
			continue
		}
		end := bytes.Index(b[loc[1]:], []byte("endstream"))
		if end < 0 {
			return 0, reject(ReasonMalformed, "the PDF has an unterminated stream")
		}
		data, err := inflate(b[loc[1]:loc[1]+end], &budget)
		if err != nil {
			if budget < 0 {
				return 0, reject(ReasonMalformed, "the PDF expands beyond %d MiB", maxInflated>>20)
			}
			continue // not FlateDecode or damaged: count what we can read
		}
		pages += len(pageObj.FindAllIndex(data, -1))
	}
	if pages == 0 {
		return 0, reject(ReasonMalformed, "no pages found in the PDF")
	}
	if pages > lim.MaxPages {
		return 0, reject(ReasonTooManyPages, "the PDF has more than %d pages", lim.MaxPages)
	}
	return pages, nil
}

// isObjStm reports whether the dictionary just before a stream keyword declares an object
// stream: it looks back to the nearest "obj".
func isObjStm(b []byte, at int) bool {
	start := bytes.LastIndex(b[:at], []byte("obj"))
	if start < 0 {
		return false
	}
	dict := b[start:at]
	return objStmTag.Match(dict) && bytes.Contains(dict, []byte("/FlateDecode"))
}

var errBudget = errors.New("inflate budget exceeded")

// inflate decompresses a zlib stream, charging its output to budget.
func inflate(data []byte, budget *int64) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, *budget+1))
	*budget -= int64(len(out))
	if *budget < 0 {
		return nil, errBudget
	}
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return out, nil
}
