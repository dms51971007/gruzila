package executor

import (
	"encoding/hex"
	"testing"

	iso8583lib "github.com/moov-io/iso8583"

	"gruzilla/internal/scenario"
)

func TestBuildPayloadFromISO8583Minimal(t *testing.T) {
	step := scenario.Step{
		TCPISO8583Spec: "spec87ascii",
		TCPISO8583Fields: map[string]string{
			"0": "0200",
			"3": "000000",
		},
	}
	vars := map[string]string{}
	b, spec, err := buildPayloadFromISO8583(step, vars)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("empty pack")
	}
	if spec == nil {
		t.Fatal("nil spec")
	}
}

// wire как в типичном Kotlin-разборе: MTI[4] + binary primary bitmap[8] + данные полей (без 16 ASCII hex на растр).
func TestSpec87ASCIIBinaryBitmapWireLayout(t *testing.T) {
	step := scenario.Step{
		TCPISO8583Spec: "spec87ascii_binmap",
		TCPISO8583Fields: map[string]string{
			"0": "0200",
			"3": "000000",
		},
	}
	b, _, err := buildPayloadFromISO8583(step, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:4]) != "0200" {
		t.Fatalf("MTI: got %q", b[:4])
	}
	if b[4]&0x80 != 0 {
		t.Fatalf("unexpected secondary bitmap (bit1 of first bitmap byte); got %02x", b[4])
	}
	if len(b) != 4+8+6 {
		t.Fatalf("len want %d (mti+8byte bitmap+fld3), got %d hex=%s", 4+8+6, len(b), hex.EncodeToString(b))
	}
	if string(b[12:]) != "000000" {
		t.Fatalf("field 3 on wire: %q", b[12:])
	}
}

func TestSpec87ASCIIBinaryBitmapUnpackRoundTrip(t *testing.T) {
	step := scenario.Step{
		TCPISO8583Spec: "spec87ascii_binmap",
		TCPISO8583Fields: map[string]string{
			"0": "0200",
			"3": "000000",
		},
	}
	packed, sp, err := buildPayloadFromISO8583(step, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg := iso8583lib.NewMessage(sp)
	if err := msg.Unpack(packed); err != nil {
		t.Fatal(err)
	}
	got, err := msg.GetString(3)
	if err != nil {
		t.Fatal(err)
	}
	// moov Numeric может отдавать без ведущих нулей
	if got != "000000" && got != "0" {
		t.Fatalf("field 3: got %q", got)
	}
}
