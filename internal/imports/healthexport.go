package imports

import (
	"archive/zip"
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/KaanEmec/vitamux/internal/connectors/applehealth"
)

// Limits bound what an Apple Health export may cost to read. Exceeding one fails the import.
type Limits struct {
	MaxXMLBytes   int64 // export.xml after decompression
	MaxRatio      int64 // export.xml decompressed : compressed size, zip only
	MaxZipEntries int   // files in the zip's directory
	MaxDepth      int   // element nesting
	MaxElements   int64 // elements in export.xml
	MaxAttrs      int   // attributes of one element
	MaxChildren   int   // metadata entries, members or statistics of one record
}

// DefaultLimits fit a decade of dense watch data (export.xml is a few GiB at most and
// compresses about 20:1).
var DefaultLimits = Limits{MaxXMLBytes: 8 << 30, MaxRatio: 100, MaxZipEntries: 200_000, MaxDepth: 8,
	MaxElements: 200_000_000, MaxAttrs: 32, MaxChildren: 512}

// ErrLimit wraps every limit violation.
var ErrLimit = errors.New("apple health export: limit exceeded")

// OpenExport opens export.xml, or the export.xml inside an export.zip, for ParseExport. In a
// zip only export.xml is decompressed (never export_cda.xml, routes or ECGs), and only up to
// the size and ratio limits, whatever the zip header claims.
func OpenExport(name string, lim Limits) (io.ReadCloser, error) {
	f, err := os.Open(name) //nolint:gosec // the operator's own file
	if err != nil {
		return nil, err
	}
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != "PK\x03\x04" {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, err
		}
		return readCloser{capped(f, lim.MaxXMLBytes), f}, nil
	}
	_ = f.Close()
	zr, err := zip.OpenReader(name)
	if err != nil {
		return nil, err
	}
	rc, err := openZipEntry(&zr.Reader, lim)
	if err != nil {
		_ = zr.Close()
		return nil, err
	}
	return readCloser{rc, closers{rc, zr}}, nil
}

func openZipEntry(zr *zip.Reader, lim Limits) (io.ReadCloser, error) {
	if len(zr.File) > lim.MaxZipEntries {
		return nil, fmt.Errorf("%w: %d files in the zip", ErrLimit, len(zr.File))
	}
	var entry *zip.File
	for _, f := range zr.File {
		if path.Base(f.Name) != "export.xml" {
			continue
		}
		if entry != nil {
			return nil, errors.New("apple health export: the zip holds more than one export.xml")
		}
		entry = f
	}
	if entry == nil {
		return nil, errors.New("apple health export: no export.xml in the zip")
	}
	max := lim.MaxXMLBytes
	if c := int64(entry.CompressedSize64); entry.Method != zip.Store && lim.MaxRatio > 0 && c < max/lim.MaxRatio { //nolint:gosec // sizes < 2^63
		max = c * lim.MaxRatio
	}
	if entry.UncompressedSize64 > uint64(max) { //nolint:gosec // max > 0
		return nil, fmt.Errorf("%w: export.xml is %d bytes, at most %d allowed", ErrLimit, entry.UncompressedSize64, max)
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	return readCloser{capped(rc, max), rc}, nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

type closers []io.Closer

func (cs closers) Close() error {
	var err error
	for _, c := range cs {
		err = errors.Join(err, c.Close())
	}
	return err
}

// capped fails reads past max bytes instead of truncating, so an oversized file is an error,
// not a silently shorter import.
func capped(r io.Reader, max int64) io.Reader { return &capReader{r: r, left: max} }

type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		var one [1]byte
		if n, _ := c.r.Read(one[:]); n > 0 {
			return 0, fmt.Errorf("%w: export.xml is larger than allowed", ErrLimit)
		}
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// ParseExport streams export.xml and calls fn with each Record, Correlation and Workout under
// the root, with its page type (a workout's is HKWorkoutTypeIdentifier). A record keeps its
// attributes, MetadataEntry children, a correlation its member Records, a workout its
// WorkoutStatistics; other children (HRV beat lists, workout events and routes) and other
// top-level elements (Me, ActivitySummary, clinical records) are skipped. The inline DTD is
// ignored and only XML's predefined entities are known, so nothing external is ever read.
func ParseExport(r io.Reader, lim Limits, fn func(typ string, rec applehealth.ExportRecord) error) error {
	d := xml.NewDecoder(bufio.NewReaderSize(r, 1<<16))
	d.Strict = true
	var (
		depth    int
		elements int64
		rec      *applehealth.ExportRecord // the record being read (depth 2)
		member   *applehealth.ExportRecord // a correlation member being read (depth 3)
		typ      string
	)
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			if depth != 0 || elements == 0 {
				return errors.New("apple health export: not a complete export.xml")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("apple health export: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			elements++
			switch {
			case depth > lim.MaxDepth:
				return fmt.Errorf("%w: elements nested deeper than %d", ErrLimit, lim.MaxDepth)
			case elements > lim.MaxElements:
				return fmt.Errorf("%w: more than %d elements", ErrLimit, lim.MaxElements)
			case len(t.Attr) > lim.MaxAttrs:
				return fmt.Errorf("%w: an element with %d attributes", ErrLimit, len(t.Attr))
			case depth == 1 && t.Name.Local != "HealthData":
				return fmt.Errorf("apple health export: root element %q, want HealthData", t.Name.Local)
			}
			name := t.Name.Local
			switch {
			case depth == 2 && (name == "Record" || name == "Correlation" || name == "Workout"):
				rec = &applehealth.ExportRecord{Attrs: attrs(t.Attr)}
				typ = rec.Attrs["type"]
				if name == "Workout" {
					typ = "HKWorkoutTypeIdentifier"
				}
			case rec == nil:
			case name == "MetadataEntry" && (depth == 3 || depth == 4 && member != nil):
				target := rec
				if depth == 4 {
					target = member
				}
				if len(target.Metadata) >= lim.MaxChildren {
					return fmt.Errorf("%w: more than %d metadata entries", ErrLimit, lim.MaxChildren)
				}
				a := attrs(t.Attr)
				if target.Metadata == nil {
					target.Metadata = map[string]string{}
				}
				target.Metadata[a["key"]] = a["value"]
			case depth == 3 && name == "Record":
				if len(rec.Records) >= lim.MaxChildren {
					return fmt.Errorf("%w: more than %d members", ErrLimit, lim.MaxChildren)
				}
				member = &applehealth.ExportRecord{Attrs: attrs(t.Attr)}
			case depth == 3 && name == "WorkoutStatistics":
				if len(rec.Statistics) >= lim.MaxChildren {
					return fmt.Errorf("%w: more than %d statistics", ErrLimit, lim.MaxChildren)
				}
				rec.Statistics = append(rec.Statistics, attrs(t.Attr))
			}
		case xml.EndElement:
			switch {
			case depth == 3 && member != nil:
				rec.Records = append(rec.Records, *member)
				member = nil
			case depth == 2 && rec != nil:
				if typ != "" {
					if err := fn(typ, *rec); err != nil {
						return err
					}
				}
				rec = nil
			}
			depth--
		}
	}
}

func attrs(as []xml.Attr) map[string]string {
	m := make(map[string]string, len(as))
	for _, a := range as {
		m[a.Name.Local] = a.Value
	}
	return m
}
