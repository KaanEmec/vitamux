package review

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/KaanEmec/vitamux/internal/documents"
)

// TestDecodePatch: every field kind accepts its valid values and names the problem of an invalid one
// by pointer and detail, never echoing the value.
func TestDecodePatch(t *testing.T) {
	for _, c := range []struct {
		name, patch string
		want        []documents.FieldProblem
	}{
		{"valid", `{"analyte_label": "Creatinine", "value_numeric": 1.5, "comparator": "<=", "collected_at": "2025-07-17", "analyte": "glucose", "unit_text": null}`, nil},
		{"unknown field", `{"nope": 1}`, []documents.FieldProblem{{Pointer: "/nope", Detail: "is not an editable field"}}},
		{"null label", `{"analyte_label": null}`, []documents.FieldProblem{{Pointer: "/analyte_label", Detail: "must not be null"}}},
		{"number as string", `{"ref_low": "x"}`, []documents.FieldProblem{{Pointer: "/ref_low", Detail: "must be a number or null"}}},
		{"string as number", `{"value_text": 5}`, []documents.FieldProblem{{Pointer: "/value_text", Detail: "must be a string or null"}}},
		{"empty text", `{"unit_text": ""}`, []documents.FieldProblem{{Pointer: "/unit_text", Detail: "must be 1-64 characters (null when not printed)"}}},
		{"long text", `{"printed_flag": "0123456789012345678901234567890123"}`, []documents.FieldProblem{{Pointer: "/printed_flag", Detail: "must be 1-32 characters (null when not printed)"}}},
		{"comparator", `{"comparator": "="}`, []documents.FieldProblem{{Pointer: "/comparator", Detail: "must be <, >, <=, >= or null"}}},
		{"date", `{"reported_at": "2025-07-17T07:02Z"}`, []documents.FieldProblem{{Pointer: "/reported_at", Detail: "must be an ISO 8601 local date or date-time without offset"}}},
		{"analyte", `{"analyte": "nope"}`, []documents.FieldProblem{{Pointer: "/analyte", Detail: "must be an analyte code from docs/analytes.md, or null for unknown"}}},
		{"sorted", `{"comparator": "=", "analyte": "nope"}`, []documents.FieldProblem{
			{Pointer: "/analyte", Detail: "must be an analyte code from docs/analytes.md, or null for unknown"},
			{Pointer: "/comparator", Detail: "must be <, >, <=, >= or null"}}},
	} {
		var body map[string]json.RawMessage
		if err := json.Unmarshal([]byte(c.patch), &body); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		set, err := decodePatch(body)
		if c.want == nil {
			if err != nil || len(set) != len(body) {
				t.Errorf("%s: %v %v", c.name, set, err)
			}
			continue
		}
		var inv *InvalidError
		if !errors.As(err, &inv) || len(inv.Problems) != len(c.want) {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		for i, p := range inv.Problems {
			if p != c.want[i] {
				t.Errorf("%s: problem %d is %+v, want %+v", c.name, i, p, c.want[i])
			}
		}
	}
}
