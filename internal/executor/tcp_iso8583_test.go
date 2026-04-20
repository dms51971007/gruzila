package executor

import (
	"encoding/xml"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		if _, ok := sub.(*field.Hex); !ok {
			t.Fatalf("field 55 subfield %s must be hex in BerTLV mode, got %T", tag, sub)
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

func TestBuildPayloadFromISO8583SpecXMLFieldIDWithPlusSuffix(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "BPC8583POS.xml")
	xmlBody := `<Protocol id="6" name="BPC8583POS" type="ISO8583">
	<Field name="MTI" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="4"></Field>
	<Field name="BITMAP" fldType="ISOBITMAP" encode="BCH" format="*" lenType="FIX" len="8">
		<Field id="3" name="F03" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="6"></Field>
		<Field id="64+" name="F64+" fldType="GENERIC" encode="ASCII" format="ans" lenType="FIX" len="8"></Field>
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
			"64": "ABCDEFGH",
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
	v, err := msg.GetString(64)
	if err != nil {
		t.Fatal(err)
	}
	if v != "ABCDEFGH" {
		t.Fatalf("field 64: got %q want %q", v, "ABCDEFGH")
	}
}

func TestBuildPayloadFromISO8583SpecXMLFieldNameFallbackID(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "BPC8583POS.xml")
	xmlBody := `<Protocol id="6" name="BPC8583POS" type="ISO8583">
	<Field name="MTI" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="4"></Field>
	<Field name="BITMAP" fldType="ISOBITMAP" encode="BCH" format="*" lenType="FIX" len="8">
		<Field id="3" name="F03" fldType="GENERIC" encode="ASCII" format="n" lenType="FIX" len="6"></Field>
	</Field>
	<Field name="F95" fldType="GENERIC" encode="ASCII" format="ans" lenType="FIX" len="12"></Field>
</Protocol>`
	if err := os.WriteFile(xmlPath, []byte(xmlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	step := scenario.Step{
		TCPISO8583SpecXML: xmlPath,
		TCPISO8583Fields: map[string]string{
			"0":  "0200",
			"3":  "000000",
			"95": "123456789012",
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
	v, err := msg.GetString(95)
	if err != nil {
		t.Fatal(err)
	}
	if v != "123456789012" {
		t.Fatalf("field 95: got %q want %q", v, "123456789012")
	}
}

func TestAuditISO8583XMLFieldByField(t *testing.T) {
	xmlPath := strings.TrimSpace(os.Getenv("BPC_XML_AUDIT_PATH"))
	if xmlPath == "" {
		t.Skip("set BPC_XML_AUDIT_PATH to run XML field-by-field audit")
	}
	data, err := os.ReadFile(xmlPath)
	if err != nil {
		t.Fatalf("read xml: %v", err)
	}
	var proto xmlProtocolSpec
	if err := xml.Unmarshal(data, &proto); err != nil {
		clean := sanitizeISO8583XML(data)
		if err2 := xml.Unmarshal(clean, &proto); err2 != nil {
			t.Fatalf("parse xml: %v", err2)
		}
	}
	sp, err := buildMessageSpecFromXMLProtocol(proto)
	if err != nil {
		t.Fatalf("build spec: %v", err)
	}

	type row struct {
		id      string
		name    string
		fldType string
		status  string
		detail  string
	}
	rows := make([]row, 0, 512)
	var walk func([]xmlFieldSpec)
	walk = func(nodes []xmlFieldSpec) {
		for _, xf := range nodes {
			idNum, ok := parseISO8583FieldID(xf.ID)
			idText := strings.TrimSpace(xf.ID)
			if idText == "" {
				idText = "-"
			}
			name := strings.TrimSpace(xf.Name)
			if name == "" {
				name = strings.TrimSpace(xf.Tag)
			}
			if name == "" {
				name = "<unnamed>"
			}

			switch {
			case strings.EqualFold(name, "MTI"), strings.EqualFold(name, "BITMAP"):
				rows = append(rows, row{id: idText, name: name, fldType: xf.FldType, status: "skip", detail: "system field"})
			case !ok:
				rows = append(rows, row{id: idText, name: name, fldType: xf.FldType, status: "warn", detail: "id not recognized"})
			default:
				f, ferr := makeFieldFromXML(xf)
				if ferr != nil {
					rows = append(rows, row{id: fmt.Sprintf("%d", idNum), name: name, fldType: xf.FldType, status: "error", detail: ferr.Error()})
				} else {
					spec := f.Spec()
					rows = append(rows, row{
						id:      fmt.Sprintf("%d", idNum),
						name:    name,
						fldType: xf.FldType,
						status:  "ok",
						detail:  fmt.Sprintf("len=%d pref=%T enc=%T", spec.Length, spec.Pref, spec.Enc),
					})
				}
			}
			if len(xf.Fields) > 0 {
				walk(xf.Fields)
			}
		}
	}
	walk(proto.Fields)

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].status != rows[j].status {
			return rows[i].status < rows[j].status
		}
		if rows[i].id != rows[j].id {
			return rows[i].id < rows[j].id
		}
		return rows[i].name < rows[j].name
	})

	okCnt, warnCnt, errCnt := 0, 0, 0
	for _, r := range rows {
		switch r.status {
		case "ok":
			okCnt++
		case "warn":
			warnCnt++
		case "error":
			errCnt++
		}
		t.Logf("[%s] id=%s name=%q type=%q %s", r.status, r.id, r.name, r.fldType, r.detail)
	}
	t.Logf("summary: parsed_spec_fields=%d audit_rows=%d ok=%d warn=%d error=%d", len(sp.Fields), len(rows), okCnt, warnCnt, errCnt)
	if errCnt > 0 {
		t.Fatalf("xml audit found %d errors", errCnt)
	}
}
