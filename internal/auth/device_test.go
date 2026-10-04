package auth

import "testing"

func TestNormalizePairingCode(t *testing.T) {
	for in, want := range map[string]string{
		"ABCD-EFGH":   "ABCDEFGH",
		"abcd efgh":   "ABCDEFGH",
		"o1il-2345":   "01112345", // Crockford: O is 0, I and L are 1
		"ABCD-EFG":    "",
		"ABCD-EFGU":   "", // U is not in the alphabet
		"ABCD-EFGH-1": "",
		"ÄBCD-EFGH":   "",
	} {
		got, ok := normalizePairingCode(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q: got %q %v, want %q", in, got, ok, want)
		}
	}
}
