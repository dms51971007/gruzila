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

func TestExpandStepPlaceholdersPAN(t *testing.T) {
	s, err := expandStepPlaceholders(`{{__pan:16}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 16 {
		t.Fatalf("len %d", len(s))
	}
	if !isAllDigits(s) {
		t.Fatalf("non-digit pan %q", s)
	}
	if !isValidLuhnPAN(s) {
		t.Fatalf("invalid luhn pan %q", s)
	}
}

func TestExpandStepPlaceholdersPANWithBIN(t *testing.T) {
	s, err := expandStepPlaceholders(`{{__pan:16:220012}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 16 {
		t.Fatalf("len %d", len(s))
	}
	if !strings.HasPrefix(s, "220012") {
		t.Fatalf("prefix mismatch: %q", s)
	}
	if !isValidLuhnPAN(s) {
		t.Fatalf("invalid luhn pan %q", s)
	}
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isValidLuhnPAN(pan string) bool {
	if len(pan) < 2 || !isAllDigits(pan) {
		return false
	}
	sum := 0
	double := false
	for i := len(pan) - 1; i >= 0; i-- {
		d := int(pan[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}
