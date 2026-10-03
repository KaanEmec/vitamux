package analytes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	// ErrUnknownAnalyte means the code is not in the catalogue.
	ErrUnknownAnalyte = errors.New("analytes: unknown analyte")
	// ErrUnknownUnit means the analyte has no conversion for the unit. The result stays
	// confirmable with its printed value and unit only.
	ErrUnknownUnit = errors.New("analytes: no conversion for this unit")
	// ErrNoConversion means the unit measures a different quantity that must never be
	// converted (Lp(a) nmol/L vs mg/dL, D-dimer DDU vs FEU).
	ErrNoConversion = errors.New("analytes: conversion refused")
)

// Canonical converts v, printed in unit, to the analyte's canonical unit and returns the
// conversion it applied (Factor 1 for the canonical unit itself), so callers can record it.
// Units match after normalization (see UnitKey). A printed value without a unit converts
// only for unitless canonical units (ratio, index, pH).
func Canonical(code string, v float64, unit string) (float64, Conversion, error) {
	an, ok := byCode[code]
	if !ok {
		return 0, Conversion{}, fmt.Errorf("%w: %q", ErrUnknownAnalyte, code)
	}
	c, err := an.conversion(unit)
	if err != nil {
		return 0, Conversion{}, err
	}
	return v*c.Factor + c.Offset, c, nil
}

// Convert converts v between two units of one analyte, through the canonical unit.
func Convert(code string, v float64, from, to string) (float64, error) {
	an, ok := byCode[code]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownAnalyte, code)
	}
	f, err := an.conversion(from)
	if err != nil {
		return 0, err
	}
	t, err := an.conversion(to)
	if err != nil {
		return 0, err
	}
	return (v*f.Factor + f.Offset - t.Offset) / t.Factor, nil
}

// conversion finds the conversion from a printed unit to the canonical one.
func (an Analyte) conversion(unit string) (Conversion, error) {
	if an.Unit == "" {
		return Conversion{}, fmt.Errorf("%w: %s has no canonical unit", ErrUnknownUnit, an.Code)
	}
	k := UnitKey(unit)
	if k == UnitKey(an.Unit) || (k == "" && unitless[an.Unit]) {
		return Conversion{Unit: an.Unit, Factor: 1}, nil
	}
	for _, c := range an.Conversions {
		if k == UnitKey(c.Unit) {
			return c, nil
		}
	}
	for _, r := range an.Refused {
		if k == UnitKey(r) {
			return Conversion{}, fmt.Errorf("%w: %s in %s is a different quantity from %s", ErrNoConversion, an.Code, r, an.Unit)
		}
	}
	return Conversion{}, fmt.Errorf("%w: %s from %q", ErrUnknownUnit, an.Code, unit)
}

var (
	powerOfTen = regexp.MustCompile(`10[e*](\d+)`)
	superDigit = map[rune]rune{'⁰': '0', '¹': '1', '²': '2', '³': '3', '⁴': '4', '⁵': '5', '⁶': '6', '⁷': '7', '⁸': '8', '⁹': '9'}
)

// UnitKey normalizes a printed unit for matching: case, spaces and parentheses are ignored,
// µ, μ, u and mcg spell micro, "10⁹", "x10^9", "10E9" and "10*9" spell a power of ten, K/µL
// and M/µL mean 10³/µL and 10⁶/µL, and mm³ means µL. Matching is case-insensitive, so a
// unit that differs from another only by case (G/L vs g/L) is not distinguished.
func UnitKey(unit string) string {
	var b strings.Builder
	prev := ""
	for _, r := range strings.TrimSpace(unit) {
		if d, ok := superDigit[r]; ok {
			if strings.HasSuffix(prev, "10") {
				b.WriteByte('^')
			}
			b.WriteRune(d)
			prev = ""
			continue
		}
		if unicode.IsSpace(r) || r == '(' || r == ')' {
			continue
		}
		if r == 'μ' { // Greek mu to the micro sign
			r = 'µ'
		}
		b.WriteString(strings.ToLower(string(r)))
		prev = b.String()
	}
	k := strings.TrimLeft(b.String(), "x×*")
	k = strings.ReplaceAll(k, "mcg", "µg")
	k = powerOfTen.ReplaceAllString(k, "10^$1")
	k = strings.ReplaceAll(k, ",", ".")
	parts := strings.Split(k, "/")
	for i, p := range parts {
		switch {
		case p == "mm3":
			parts[i] = "µl"
		case len(p) > 1 && p[0] == 'u' && (strings.ContainsRune("mgl", rune(p[1])) || strings.HasPrefix(p[1:], "iu") || strings.HasPrefix(p[1:], "kat")):
			parts[i] = "µ" + p[1:]
		}
	}
	if len(parts) == 2 && parts[1] == "µl" {
		switch parts[0] {
		case "k":
			parts[0] = "10^3"
		case "m":
			parts[0] = "10^6"
		}
	}
	return strings.Join(parts, "/")
}

// LabelKey normalizes a printed analyte label for alias matching: lower case, with runs
// of spaces and punctuation other than / and % collapsed to one space.
func LabelKey(label string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(label) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '/' || r == '%' {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		space = true
	}
	return b.String()
}
