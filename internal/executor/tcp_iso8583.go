package executor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	iso8583lib "github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/prefix"
	"github.com/moov-io/iso8583/specs"

	"gruzilla/internal/scenario"
)

// spec87ascii_binmap — как spec87ascii (поля в ASCII на проводе), но primary/secondary bitmap
// — сырые байты (8+8…), как в типичном Kotlin/Java разборе: MTI[4] + binary bitmap, а не 16 hex-символов.
var (
	spec87asciiBinMap     *iso8583lib.MessageSpec
	spec87asciiBinMapOnce sync.Once
)

func spec87ASCIIBinaryBitmap() *iso8583lib.MessageSpec {
	spec87asciiBinMapOnce.Do(func() {
		base := specs.Spec87ASCII
		out := make(map[int]field.Field, len(base.Fields))
		for k, v := range base.Fields {
			out[k] = v
		}
		out[1] = field.NewBitmap(&field.Spec{
			Description: "Bitmap",
			Enc:         encoding.Binary,
			Pref:        prefix.Binary.Fixed,
		})
		spec87asciiBinMap = &iso8583lib.MessageSpec{
			Name:   "ISO 8583 v1987 ASCII (binary bitmap)",
			Fields: out,
		}
	})
	return spec87asciiBinMap
}

func iso8583MessageSpecByName(name string) (*iso8583lib.MessageSpec, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "spec87ascii":
		return specs.Spec87ASCII, nil
	case "spec87ascii_binmap", "spec87_ascii_binary_bitmap":
		return spec87ASCIIBinaryBitmap(), nil
	case "spec87hex":
		return specs.Spec87Hex, nil
	case "spec87track2":
		return specs.Spec87Track2, nil
	default:
		return nil, fmt.Errorf("unknown tcp_iso8583_spec %q (spec87ascii, spec87ascii_binmap, spec87hex, spec87track2)", name)
	}
}

func buildPayloadFromISO8583(step scenario.Step, vars map[string]string) ([]byte, *iso8583lib.MessageSpec, error) {
	specName := strings.TrimSpace(step.TCPISO8583Spec)
	if specName == "" {
		specName = "spec87ascii"
	}
	sp, err := iso8583MessageSpecByName(specName)
	if err != nil {
		return nil, nil, err
	}
	msg := iso8583lib.NewMessage(sp)
	type fieldRow struct {
		id  int
		tpl string
	}
	rows := make([]fieldRow, 0, len(step.TCPISO8583Fields))
	for key, tpl := range step.TCPISO8583Fields {
		fid, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || fid < 0 {
			return nil, nil, fmt.Errorf("tcp_iso8583_fields: invalid field id %q", key)
		}
		rows = append(rows, fieldRow{id: fid, tpl: tpl})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	for _, row := range rows {
		val, err := expandStepPlaceholders(interpolate(vars, row.tpl))
		if err != nil {
			return nil, nil, fmt.Errorf("tcp_iso8583_fields[%d]: %w", row.id, err)
		}
		if err := msg.Field(row.id, val); err != nil {
			return nil, nil, fmt.Errorf("iso8583 field %d: %w", row.id, err)
		}
	}
	packed, err := msg.Pack()
	if err != nil {
		return nil, nil, fmt.Errorf("iso8583 pack: %w", err)
	}
	return packed, sp, nil
}

func tcpResponseISO8583Spec(step scenario.Step, buildSpec *iso8583lib.MessageSpec) (*iso8583lib.MessageSpec, error) {
	need := len(step.TCPISO8583Extract) > 0 || len(step.TCPISO8583Assert) > 0
	if !need {
		return nil, nil
	}
	if buildSpec != nil {
		return buildSpec, nil
	}
	name := strings.TrimSpace(step.TCPISO8583Spec)
	if name == "" {
		name = "spec87ascii"
	}
	return iso8583MessageSpecByName(name)
}

func applyTCPISO8583ExtractVars(msg *iso8583lib.Message, extract map[string]string, vars map[string]string) error {
	for varName, fieldKey := range extract {
		name := strings.TrimSpace(varName)
		if name == "" {
			continue
		}
		fid, err := strconv.Atoi(strings.TrimSpace(interpolate(vars, fieldKey)))
		if err != nil {
			return fmt.Errorf("tcp_iso8583_extract %q: invalid field id %q", name, fieldKey)
		}
		s, err := msg.GetString(fid)
		if err != nil {
			return fmt.Errorf("tcp_iso8583_extract %q (field %d): %w", name, fid, err)
		}
		vars[name] = normalizeISO8583NumericExtract(fid, s)
	}
	return nil
}

func applyTCPISO8583Assert(msg *iso8583lib.Message, assert map[string]string, vars map[string]string) error {
	for fieldKey, wantTpl := range assert {
		fid, err := strconv.Atoi(strings.TrimSpace(fieldKey))
		if err != nil {
			return fmt.Errorf("tcp_iso8583_assert: invalid field %q", fieldKey)
		}
		got, err := msg.GetString(fid)
		if err != nil {
			return fmt.Errorf("tcp_iso8583_assert field %d: %w", fid, err)
		}
		want := strings.TrimSpace(interpolate(vars, wantTpl))
		if !iso8583AssertValuesEqual(got, want) {
			return fmt.Errorf("tcp_iso8583_assert field %d: got %q want %q", fid, got, want)
		}
	}
	return nil
}

// Частые фиксированные числовые поля 87ASCII: moov GetString может отдавать без ведущих нулей.
var iso8583NumericFieldWidth = map[int]int{
	3: 6, 4: 12, 7: 10, 11: 6, 12: 6, 13: 4, 14: 4,
	18: 4, 24: 3, 25: 2, 37: 12, 39: 2, 41: 8, 42: 15, 49: 3, 70: 3,
}

func normalizeISO8583NumericExtract(fid int, s string) string {
	s = strings.TrimSpace(s)
	w, ok := iso8583NumericFieldWidth[fid]
	if !ok || w <= 0 || s == "" {
		return s
	}
	if !stringIsAllASCII(s) || !stringIsAllDigits(s) {
		return s
	}
	return padLeftDigits(s, w)
}

func padLeftDigits(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat("0", width-len(s)) + s
}

func iso8583AssertValuesEqual(got, want string) bool {
	got, want = strings.TrimSpace(got), strings.TrimSpace(want)
	if got == want {
		return true
	}
	if !stringIsAllDigits(got) || !stringIsAllDigits(want) {
		return false
	}
	w := max(len(got), len(want))
	return padLeftDigits(got, w) == padLeftDigits(want, w)
}

func stringIsAllASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

func stringIsAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
