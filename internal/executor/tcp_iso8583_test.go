package executor

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	iso8583lib "github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/prefix"

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

func TestISO8583AssertToleratesLeadingZeros(t *testing.T) {
	if !iso8583AssertValuesEqual("0", "00") || !iso8583AssertValuesEqual("00", "0") {
		t.Fatal("digit assert should treat 0 and 00 as equal")
	}
	if iso8583AssertValuesEqual("01", "10") {
		t.Fatal("distinct codes must not match")
	}
}

func TestNormalizeISO8583ExtractField39(t *testing.T) {
	if got := normalizeISO8583NumericExtract(39, "0"); got != "00" {
		t.Fatalf("field 39 extract: got %q want 00", got)
	}
}

func TestISO8583UnpackMTIField0(t *testing.T) {
	step := scenario.Step{
		TCPISO8583Spec: "spec87ascii_binmap",
		TCPISO8583Fields: map[string]string{
			"0":  "0210",
			"39": "00",
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
	mti, err := msg.GetString(0)
	if err != nil {
		t.Fatal(err)
	}
	if mti != "0210" {
		t.Fatalf("GetString(0): got %q", mti)
	}
}

func TestBuildPayloadFromISO8583SpecXML(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "BPC8583POS.xml")
	xmlBody := `<Protocol id="6" name="BPC8583POS" type="ISO8583">
	<Field name="MTI" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="4"></Field>
	<Field name="BITMAP" fldType="ISOBITMAP" encode="BCH" format="*" lenType="FIX" len="8">
		<Field id="3" name="F03" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="6"></Field>
	</Field>
</Protocol>`
	if err := os.WriteFile(xmlPath, []byte(xmlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	step := scenario.Step{
		TCPISO8583SpecXML: xmlPath,
		TCPISO8583Fields: map[string]string{
			"0": "0200",
			"3": "000000",
		},
	}
	b, spec, err := buildPayloadFromISO8583(step, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec == nil {
		t.Fatal("nil spec")
	}
	if string(b[:4]) != "0200" {
		t.Fatalf("MTI: got %q", b[:4])
	}
	if len(b) != 4+8+6 {
		t.Fatalf("len want %d (mti+8byte bitmap+fld3), got %d", 4+8+6, len(b))
	}
	if string(b[12:]) != "000000" {
		t.Fatalf("field 3 on wire: %q", b[12:])
	}
}

func TestBuildPayloadFromISO8583SpecXMLTLVField48(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "BPC8583POS.xml")
	xmlBody := `<Protocol id="6" name="BPC8583POS" type="ISO8583">
	<Field name="MTI" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="4"></Field>
	<Field name="BITMAP" fldType="ISOBITMAP" encode="BCH" format="*" lenType="FIX" len="8">
		<Field id="3" name="F03" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="6"></Field>
		<Field id="48" name="F48" fldType="TLV" encode="ASCII" format="ans" lenType="LLLVAR" len="999" tagType="ASCII" tagTypeLen="3">
			<Field name="F48.001" tag="001" fldType="GENERIC" encode="ASCII" format="ans" lenType="LLLVAR" len="255"></Field>
		</Field>
	</Field>
</Protocol>`
	if err := os.WriteFile(xmlPath, []byte(xmlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	step := scenario.Step{
		TCPISO8583SpecXML: xmlPath,
		TCPISO8583Fields: map[string]string{
			"0":  "0200",
			"3":  "000000",
			"48": `{"001":"ABC"}`,
		},
	}
	b, spec, err := buildPayloadFromISO8583(step, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec == nil || len(b) == 0 {
		t.Fatal("expected packed message with xml tlv spec")
	}
	msg := iso8583lib.NewMessage(spec)
	if err := msg.Unpack(b); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPayloadFromISO8583SpecXMLBerTLVField55(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "BPC8583POS.xml")
	xmlBody := `<Protocol id="6" name="BPC8583POS" type="ISO8583">
	<Field name="MTI" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="4"></Field>
	<Field name="BITMAP" fldType="ISOBITMAP" encode="BCH" format="*" lenType="FIX" len="8">
		<Field id="3" name="F03" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="6"></Field>
		<Field id="55" name="F55" fldType="TLV" encode="ASCII" format="ans" lenType="LLLVAR" len="999" tagType="BerTLVTag">
			<Field name="F55.9F1A" tag="9F1A" fldType="GENERIC" encode="BCH" format="b" lenType="FIX" len="2"></Field>
			<Field name="F55.5F24" tag="5F24" fldType="GENERIC" encode="BCH" format="b" lenType="FIX" len="3"></Field>
		</Field>
	</Field>
</Protocol>`
	if err := os.WriteFile(xmlPath, []byte(xmlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	step := scenario.Step{
		TCPISO8583SpecXML: xmlPath,
		TCPISO8583Fields: map[string]string{
			"0":  "0200",
			"3":  "000000",
			"55": `{"9F1A":"643","5F24":"241231"}`,
		},
	}
	_, spec, err := buildPayloadFromISO8583(step, nil)
	if err != nil {
		t.Fatal(err)
	}
	c55, ok := spec.Fields[55].(*field.Composite)
	if !ok {
		t.Fatalf("field 55 must be composite, got %T", spec.Fields[55])
	}
	if c55.Spec().Tag.Enc != encoding.BerTLVTag {
		t.Fatalf("field 55 tag encoding must be BerTLVTag")
	}
	for _, tag := range []string{"9F1A", "5F24"} {
		sub := c55.Spec().Subfields[tag]
		if sub == nil {
			t.Fatalf("field 55 subfield %s missing", tag)
		}
		if sub.Spec().Pref != prefix.BerTLV {
			t.Fatalf("field 55 subfield %s prefix must be BerTLV", tag)
		}
	}
}

func TestBuildPayloadFromISO8583SpecXMLField65OutsideBitmap(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "BPC8583POS.xml")
	xmlBody := `<Protocol id="6" name="BPC8583POS" type="ISO8583">
	<Field name="MTI" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="4"></Field>
	<Field name="BITMAP" fldType="ISOBITMAP" encode="BCH" format="*" lenType="FIX" len="8">
		<Field id="3" name="F03" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="6"></Field>
	</Field>
	<Field id="66" name="F66" fldType="GENERIC" encode="ASCII" format="ans" lenType="FIX" len="2"></Field>
</Protocol>`
	if err := os.WriteFile(xmlPath, []byte(xmlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	step := scenario.Step{
		TCPISO8583SpecXML: xmlPath,
		TCPISO8583Fields: map[string]string{
			"0":  "0200",
			"3":  "000000",
			"66": "AB",
		},
	}
	b, spec, err := buildPayloadFromISO8583(step, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg := iso8583lib.NewMessage(spec)
	if err := msg.Unpack(b); err != nil {
		t.Fatal(err)
	}
	v, err := msg.GetString(66)
	if err != nil {
		t.Fatal(err)
	}
	if v != "AB" {
		t.Fatalf("field 66: got %q want %q", v, "AB")
	}
}
