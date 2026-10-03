package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
)

const maxShapeDepth = 256

// ShapePaths returns the sorted, distinct key paths of a JSON document with the kind of value
// at each, e.g. `$."samples"[]."value":number`. Keys are JSON-quoted, array elements share
// `[]`, and containers are listed too, so a retyped field shows up as a changed path.
func ShapePaths(body []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	set := map[string]struct{}{}
	if err := walkShape(dec, "$", 0, set); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("ingest: trailing data after the JSON value")
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths, nil
}

// ShapeFingerprint is "v1:" + hex SHA-256 of the newline-joined ShapePaths. Connectors and
// normalizers compare it to detect schema drift (docs/architecture/connectors.md).
func ShapeFingerprint(body []byte) (string, error) {
	paths, err := ShapePaths(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(strings.Join(paths, "\n")))
	return "v1:" + hex.EncodeToString(sum[:]), nil
}

func walkShape(dec *json.Decoder, path string, depth int, set map[string]struct{}) error {
	if depth > maxShapeDepth {
		return errors.New("ingest: JSON nested too deeply")
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Delim:
		if v == '{' {
			set[path+":object"] = struct{}{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				quoted, _ := json.Marshal(key.(string))
				if err := walkShape(dec, path+"."+string(quoted), depth+1, set); err != nil {
					return err
				}
			}
		} else {
			set[path+":array"] = struct{}{}
			for dec.More() {
				if err := walkShape(dec, path+"[]", depth+1, set); err != nil {
					return err
				}
			}
		}
		_, err = dec.Token() // closing delimiter
		return err
	case string:
		set[path+":string"] = struct{}{}
	case json.Number:
		set[path+":number"] = struct{}{}
	case bool:
		set[path+":boolean"] = struct{}{}
	case nil:
		set[path+":null"] = struct{}{}
	}
	return nil
}
