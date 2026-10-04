package main

import (
	"testing"
)

func TestCompare(t *testing.T) {
	truth, err := truthFor("lab-01")
	if err != nil {
		t.Fatal(err)
	}
	if p, r := new(compare(truth.Rows, truth.Rows)).overall(); p != 1 || r != 1 {
		t.Fatalf("truth against itself: %v %v", p, r)
	}

	got := append(truth.Rows[:0:0], truth.Rows...)
	got[0].UnitText = new("mmol/L") // wrong value: one given, not correct
	got[1].PrintedFlag = new("H")   // extra value where none is printed
	got = got[:len(got)-1]          // a missed row
	s := compare(truth.Rows, got)
	if s.matched != len(truth.Rows)-1 || s.extra != 0 {
		t.Fatalf("matched %d extra %d", s.matched, s.extra)
	}
	if c := s.field("unit_text"); c.correct != c.truth-2 || c.predicted != c.truth-1 {
		t.Errorf("unit_text: %+v", *c)
	}
	if c := s.field("printed_flag"); c.predicted != c.correct+1 {
		t.Errorf("printed_flag: %+v", *c)
	}
	if c := s.field("analyte_label"); c.truth != len(truth.Rows) || c.correct != len(truth.Rows)-1 {
		t.Errorf("analyte_label: %+v", *c)
	}
}
