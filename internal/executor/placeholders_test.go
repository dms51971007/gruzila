package executor

import (
	"strings"
	"testing"
)

func TestExpandStepPlaceholdersRandDigits(t *testing.T) {
	s, err := expandStepPlaceholders(`x{{__randDigits:4}}y`)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 6 || s[0] != 'x' || s[5] != 'y' {
		t.Fatalf("got %q", s)
	}
	for i := 1; i <= 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			t.Fatalf("non-digit at %d: %q", i, s)
		}
	}
}

func TestExpandStepPlaceholdersRandHex(t *testing.T) {
	s, err := expandStepPlaceholders(`{{__randHex:6}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 6 {
		t.Fatalf("len %d", len(s))
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("bad char %q in %q", c, s)
		}
	}
}
