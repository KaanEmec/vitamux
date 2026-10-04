package analytes

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

func TestConversions(t *testing.T) {
	for _, tc := range []struct {
		code string
		v    float64
		unit string
		want float64
	}{
		{"glucose", 90, "mg/dL", 4.9959},
		{"glucose", 90, "mg/dl", 4.9959},
		{"glucose", 5.4, "mmol/L", 5.4},
		{"hemoglobin", 14.2, "g/dL", 142},
		{"hemoglobin", 8.8, "mmol/L", 141.768},
		{"creatinine", 1.1, "mg/dL", 97.262},
		{"creatinine", 1.1, "mg/dL", 97.262},
		{"creatinine", 80, "umol/L", 80},
		{"creatinine", 80, "μmol/L", 80}, // Greek mu
		{"wbc", 6.1, "10^3/uL", 6.1},
		{"wbc", 6.1, "K/µL", 6.1},
		{"wbc", 6.1, "x10E9/L", 6.1},
		{"wbc", 6.1, "10*9/l", 6.1},
		{"hematocrit", 0.42, "L/L", 42},
		{"alt", 0.5, "µkat/L", 30},
		{"alt", 0.5, "ukat/l", 30},
		{"tsh", 2.1, "uIU/mL", 2.1},
		{"vitamin_b12", 400, "pg/mL", 295.12},
		{"vitamin_d_25oh", 30, "ng/mL", 74.88},
		{"lpa_mass", 300, "mg/L", 30},
		{"d_dimer", 0.4, "µg/mL FEU", 0.4},
		{"d_dimer", 0.4, "mg/L (FEU)", 0.4},
		{"egfr", 95, "mL/min/1,73 m2", 95},
		{"inr", 1.1, "", 1.1}, // unitless printed without a unit
		{"urine_acr", 30, "mg/g", 3.39},
		{"selenium", 100, "mcg/L", 1.266},
		{"hba1c", 5.0, "%", 31.14765}, // affine: (5 - 2.15) * 10.929
		{"hba1c", 6.5, "% (NGSP)", 47.54115},
		{"hba1c", 48, "mmol/mol", 48},
	} {
		got, c, err := Canonical(tc.code, tc.v, tc.unit)
		if err != nil {
			t.Errorf("%s %v %s: %v", tc.code, tc.v, tc.unit, err)
			continue
		}
		if !near(got, tc.want) {
			t.Errorf("%s %v %s = %v, want %v", tc.code, tc.v, tc.unit, got, tc.want)
		}
		if c.Factor == 0 || c.Unit == "" {
			t.Errorf("%s %s: conversion not reported: %+v", tc.code, tc.unit, c)
		}
	}
}

func TestConvertRoundTrip(t *testing.T) {
	mmol, err := Convert("glucose", 100, "mg/dL", "mmol/L")
	if err != nil || !near(mmol, 5.551) {
		t.Fatalf("glucose mg/dL -> mmol/L = %v, %v", mmol, err)
	}
	back, err := Convert("glucose", mmol, "mmol/L", "mg/dL")
	if err != nil || !near(back, 100) {
		t.Fatalf("glucose mmol/L -> mg/dL = %v, %v", back, err)
	}
	ngsp, err := Convert("hba1c", 53, "mmol/mol", "%")
	if err != nil || math.Abs(ngsp-7.0) > 0.01 {
		t.Fatalf("hba1c 53 mmol/mol -> %% = %v, %v", ngsp, err)
	}
	ifcc, err := Convert("hba1c", ngsp, "%", "mmol/mol")
	if err != nil || !near(ifcc, 53) {
		t.Fatalf("hba1c round trip = %v, %v", ifcc, err)
	}
	if hba1c.Offset != -2.15*hba1c.Factor && !near(hba1c.Offset, -2.15*hba1c.Factor) {
		t.Fatalf("hba1c offset %v is not -2.15 * factor", hba1c.Offset)
	}
}

func TestRefusedAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		code, unit string
		want       error
	}{
		{"lpa_molar", "mg/dL", ErrNoConversion},
		{"lpa_molar", "mg/L", ErrNoConversion},
		{"lpa_mass", "nmol/L", ErrNoConversion},
		{"d_dimer", "ng/mL DDU", ErrNoConversion},
		{"d_dimer", "µg/mL DDU", ErrNoConversion},
		{"d_dimer", "mg/L", ErrUnknownUnit}, // FEU or DDU not printed: no conversion
		{"glucose", "mg/L", ErrUnknownUnit},
		{"glucose", "", ErrUnknownUnit},
		{"vitamin_b6", "nmol/L", ErrUnknownUnit}, // kept as printed
		{"ana", "titre", ErrUnknownUnit},
		{"not_an_analyte", "mg/dL", ErrUnknownAnalyte},
	} {
		if _, _, err := Canonical(tc.code, 1, tc.unit); !errors.Is(err, tc.want) {
			t.Errorf("%s from %q: err %v, want %v", tc.code, tc.unit, err, tc.want)
		}
	}
	if _, err := Convert("lpa_mass", 30, "mg/dL", "nmol/L"); !errors.Is(err, ErrNoConversion) {
		t.Errorf("lpa mg/dL -> nmol/L: %v", err)
	}
}

func TestUnitKey(t *testing.T) {
	for in, want := range map[string]string{
		"10⁹/L": "10^9/l", "10¹²/L": "10^12/l", "×10^9/L": "10^9/l", "10E9/L": "10^9/l",
		"K/uL": "10^3/µl", "M/µL": "10^6/µl", "/mm3": "/µl", "U/L": "u/l", "uIU/mL": "µiu/ml",
		"mL/min/1.73m²": "ml/min/1.73m2", "mg/L FEU": "mg/lfeu", " mmol / L ": "mmol/l",
	} {
		if got := UnitKey(in); got != want {
			t.Errorf("UnitKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLabelKey(t *testing.T) {
	for in, want := range map[string]string{
		"LDL-C": "ldl c", "ldl_c": "ldl c", "  Vitamin D (25-OH) ": "vitamin d 25 oh", "A/G ratio": "a/g ratio",
		"Anti-Müllerian hormone": "anti müllerian hormone",
	} {
		if got := LabelKey(in); got != want {
			t.Errorf("LabelKey(%q) = %q, want %q", in, got, want)
		}
	}
}

var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestCatalogueIntegrity(t *testing.T) {
	codes := map[string]bool{}
	labels := map[string]string{}
	for _, an := range All() {
		if !codeRe.MatchString(an.Code) || codes[an.Code] {
			t.Errorf("%s: invalid or duplicate code", an.Code)
		}
		codes[an.Code] = true
		if an.Name == "" || an.Section == "" {
			t.Errorf("%s: missing name or section", an.Code)
		}
		units := map[string]bool{UnitKey(an.Unit): true}
		for _, c := range an.Conversions {
			if an.Unit == "" || c.Factor <= 0 || units[UnitKey(c.Unit)] {
				t.Errorf("%s: invalid or duplicate conversion %v", an.Code, c)
			}
			units[UnitKey(c.Unit)] = true
		}
		for _, r := range an.Refused {
			if units[UnitKey(r)] {
				t.Errorf("%s: %s is both convertible and refused", an.Code, r)
			}
		}
		for _, l := range an.SeedAliases() {
			k := LabelKey(l)
			if other, dup := labels[k]; dup {
				t.Errorf("seed alias %q maps to both %s and %s", l, other, an.Code)
			}
			labels[k] = an.Code
		}
		if an.LOINC != "" && !validLOINC(an.LOINC) {
			t.Errorf("%s: LOINC %q fails the format or check digit", an.Code, an.LOINC)
		}
	}
	// The no-conversion pairs of analyte-catalog.md.
	for code, unit := range map[string]string{"lpa_molar": "mg/dL", "lpa_mass": "nmol/L", "d_dimer": "mg/L DDU"} {
		if an, _ := Lookup(code); !containsKey(an.Refused, unit) {
			t.Errorf("%s must refuse %s", code, unit)
		}
	}
}

func containsKey(units []string, u string) bool {
	for _, x := range units {
		if UnitKey(x) == UnitKey(u) {
			return true
		}
	}
	return false
}

// validLOINC checks the LOINC format and its mod-10 check digit. It cannot prove a code is
// right for the analyte: codes are entered only from a verified loinc.org lookup.
func validLOINC(s string) bool {
	body, check, ok := strings.Cut(s, "-")
	if !ok || len(check) != 1 || len(body) == 0 || len(body) > 7 {
		return false
	}
	sum := 0
	for i := range body {
		d := int(body[len(body)-1-i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if i%2 == 0 {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return strconv.Itoa((10-sum%10)%10) == check
}

func TestValidLOINC(t *testing.T) {
	// Synthetic check-digit cases: the algorithm, not catalogue data.
	for s, want := range map[string]bool{"12345-6": false, "1234-0": false, "1-8": true, "10-6": false, "10-9": true, "2345-7": true, "x-1": false} {
		if got := validLOINC(s); got != want {
			t.Errorf("validLOINC(%q) = %v", s, got)
		}
	}
}

// TestGeneratedFilesUpToDate is the drift check: regenerate in memory and compare with the committed files.
func TestGeneratedFilesUpToDate(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	seed, err := SeedPath(root)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{seed: SeedSQL(), filepath.Join(root, DocPath): Doc()} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run: go run ./internal/documents/analytes/gen)", path, err)
		}
		if string(got) != want {
			t.Errorf("%s is stale; run: go run ./internal/documents/analytes/gen", path)
		}
	}
}
