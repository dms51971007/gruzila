package executor

import "testing"

func TestEncodeTCPPayloadTextISO88591(t *testing.T) {
	// U+00E9 LATIN SMALL LETTER E WITH ACUTE -> single byte 0xE9
	s := "a\xe9b" // UTF-8 string: in Go source \xe9 in string is one rune U+00E9 if valid... actually "\xe9" alone might be invalid UTF-8
	// use explicit rune
	s = "a" + string(rune(0xE9)) + "b"
	b, err := encodeTCPPayloadText(s, "iso8859_1")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{'a', 0xE9, 'b'}
	if len(b) != len(want) {
		t.Fatalf("len %d want %d", len(b), len(want))
	}
	for i := range want {
		if b[i] != want[i] {
			t.Fatalf("byte %d: got %x want %x", i, b[i], want[i])
		}
	}
	if len(b) != 3 {
		t.Fatalf("latin1 len want 3 got %d", len(b))
	}
	utf := []byte(s)
	if len(utf) != 4 {
		t.Fatalf("utf8 len of a+é+b want 4 got %d", len(utf))
	}
}
