package executor

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	stdsort "sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	iso8583lib "github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/prefix"
	isoSort "github.com/moov-io/iso8583/sort"
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

type xmlSpecCacheEntry struct {
	modTime time.Time
	size    int64
	spec    *iso8583lib.MessageSpec
}

var iso8583XMLSpecCache sync.Map // key: absolute path, value: xmlSpecCacheEntry

type xmlProtocolSpec struct {
	Name   string         `xml:"name,attr"`
	Fields []xmlFieldSpec `xml:"Field"`
}

type xmlFieldSpec struct {
	ID         string         `xml:"id,attr"`
	Name       string         `xml:"name,attr"`
	Tag        string         `xml:"tag,attr"`
	Desc       string         `xml:"desc,attr"`
	FldType    string         `xml:"fldType,attr"`
	Encode     string         `xml:"encode,attr"`
	Format     string         `xml:"format,attr"`
	LenType    string         `xml:"lenType,attr"`
	Len        string         `xml:"len,attr"`
	TagType    string         `xml:"tagType,attr"`
	TagTypeLen string         `xml:"tagTypeLen,attr"`
	Range      xmlLengthRange `xml:"LengthRange"`
	Fields     []xmlFieldSpec `xml:"Field"`
}

type xmlLengthRange struct {
	Min string `xml:"minLength,attr"`
	Max string `xml:"maxLength,attr"`
}

func resolveISO8583Spec(step scenario.Step, vars map[string]string) (*iso8583lib.MessageSpec, error) {
	xmlPath := strings.TrimSpace(interpolate(vars, step.TCPISO8583SpecXML))
	if xmlPath != "" {
		return loadISO8583SpecFromXML(xmlPath)
	}
	specName := strings.TrimSpace(step.TCPISO8583Spec)
	if specName == "" {
		specName = "spec87ascii"
	}
	return iso8583MessageSpecByName(specName)
}

func loadISO8583SpecFromXML(path string) (*iso8583lib.MessageSpec, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("tcp_iso8583_spec_xml: resolve %q: %w", path, err)
	}
	st, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("tcp_iso8583_spec_xml: stat %q: %w", absPath, err)
	}
	if cached, ok := iso8583XMLSpecCache.Load(absPath); ok {
		entry, ok := cached.(xmlSpecCacheEntry)
		if ok && entry.size == st.Size() && entry.modTime.Equal(st.ModTime()) && entry.spec != nil {
			return entry.spec, nil
		}
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("tcp_iso8583_spec_xml: read %q: %w", absPath, err)
	}
	var proto xmlProtocolSpec
	if err := xml.Unmarshal(data, &proto); err != nil {
		// В реальных выгрузках встречаются "грязные" XML (битые атрибуты, неэкранированный '<'
		// внутри desc и т.п.). Пробуем мягкую санацию и парсим повторно.
		clean := sanitizeISO8583XML(data)
		if err2 := xml.Unmarshal(clean, &proto); err2 != nil {
			return nil, fmt.Errorf("tcp_iso8583_spec_xml: parse %q: %w", absPath, err2)
		}
	}
	sp, err := buildMessageSpecFromXMLProtocol(proto)
	if err != nil {
		return nil, fmt.Errorf("tcp_iso8583_spec_xml: %w", err)
	}
	iso8583XMLSpecCache.Store(absPath, xmlSpecCacheEntry{
		modTime: st.ModTime(),
		size:    st.Size(),
		spec:    sp,
	})
	return sp, nil
}

func sanitizeISO8583XML(data []byte) []byte {
	s := string(data)
	// Частый дефект: слитный атрибут вместо имени тега + атрибута.
	s = strings.ReplaceAll(s, "<ProtocolFieldPaddingside=", "<ProtocolFieldPadding side=")
	// Невалидный XML: необработанный '<' внутри значений атрибутов (обычно desc="... < ...").
	s = escapeLessThanInQuotedAttributes(s)
	// На всякий случай удаляем некорректные UTF-8 последовательности.
	if !utf8.ValidString(s) {
		out := make([]rune, 0, len(s))
		for _, r := range s {
			if r == utf8.RuneError {
				continue
			}
			out = append(out, r)
		}
		s = string(out)
	}
	return []byte(s)
}

func escapeLessThanInQuotedAttributes(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 64)
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inQuote = !inQuote
			b.WriteByte(ch)
			continue
		}
		if inQuote && ch == '<' {
			b.WriteString("&lt;")
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func buildMessageSpecFromXMLProtocol(proto xmlProtocolSpec) (*iso8583lib.MessageSpec, error) {
	var bitmap *xmlFieldSpec
	for i := range proto.Fields {
		f := &proto.Fields[i]
		if strings.EqualFold(strings.TrimSpace(f.Name), "BITMAP") {
			bitmap = f
			break
		}
	}
	if bitmap == nil {
		return nil, fmt.Errorf("xml protocol has no BITMAP field")
	}
	fields := make(map[int]field.Field)
	fields[0] = field.NewString(&field.Spec{
		Length:      4,
		Description: "MTI",
		Enc:         encoding.ASCII,
		Pref:        prefix.ASCII.Fixed,
	})
	fields[1] = field.NewBitmap(&field.Spec{
		Description: "Bitmap",
		Enc:         encoding.Binary,
		Pref:        prefix.Binary.Fixed,
	})
	for _, xf := range bitmap.Fields {
		id, err := strconv.Atoi(strings.TrimSpace(xf.ID))
		if err != nil || id <= 1 {
			continue
		}
		ff, err := makeFieldFromXML(xf)
		if err != nil {
			return nil, fmt.Errorf("field id=%d name=%q: %w", id, xf.Name, err)
		}
		fields[id] = ff
	}
	// Некоторые выгрузки описывают дополнительные поля (например 65+) вне BITMAP.
	// Подхватываем их тоже, если это валидные ISO-поля.
	for _, xf := range proto.Fields {
		id, err := strconv.Atoi(strings.TrimSpace(xf.ID))
		if err != nil || id <= 1 {
			continue
		}
		if _, exists := fields[id]; exists {
			continue
		}
		ff, err := makeFieldFromXML(xf)
		if err != nil {
			return nil, fmt.Errorf("field id=%d name=%q: %w", id, xf.Name, err)
		}
		fields[id] = ff
	}
	name := strings.TrimSpace(proto.Name)
	if name == "" {
		name = "ISO8583 from XML"
	}
	return &iso8583lib.MessageSpec{Name: name, Fields: fields}, nil
}

func makeFieldFromXML(xf xmlFieldSpec) (field.Field, error) {
	if strings.EqualFold(strings.TrimSpace(xf.FldType), "TLV") && len(xf.Fields) > 0 {
		return makeTLVFieldFromXML(xf)
	}
	rawLen := strings.TrimSpace(xf.Len)
	if rawLen == "" {
		// У некоторых выгрузок len отсутствует, но указан maxLength в LengthRange.
		rawLen = strings.TrimSpace(xf.Range.Max)
	}
	length, err := strconv.Atoi(rawLen)
	if err != nil || length < 0 {
		return nil, fmt.Errorf("invalid len %q", xf.Len)
	}
	enc := xmlEncodeToMoov(strings.TrimSpace(xf.Encode))
	pref, err := xmlLenTypeToMoovPrefix(strings.TrimSpace(xf.LenType), enc)
	if err != nil {
		return nil, err
	}
	spec := &field.Spec{
		Length:      length,
		Description: strings.TrimSpace(xf.Desc),
		Enc:         enc,
		Pref:        pref,
	}
	format := strings.ToLower(strings.TrimSpace(xf.Format))
	switch {
	case strings.HasPrefix(format, "b"), strings.EqualFold(strings.TrimSpace(xf.Encode), "BCH"), strings.EqualFold(strings.TrimSpace(xf.Encode), "BCD"):
		return field.NewBinary(spec), nil
	default:
		// В runtime мы подаем значения как строки из сценария; String сохраняет ведущие нули
		// (например F03=000000), тогда как Numeric может схлопывать их.
		return field.NewString(spec), nil
	}
}

func makeTLVFieldFromXML(xf xmlFieldSpec) (field.Field, error) {
	rawLen := strings.TrimSpace(xf.Len)
	if rawLen == "" {
		rawLen = strings.TrimSpace(xf.Range.Max)
	}
	length, err := strconv.Atoi(rawLen)
	if err != nil || length < 0 {
		return nil, fmt.Errorf("invalid len %q", xf.Len)
	}
	enc := xmlEncodeToMoov(strings.TrimSpace(xf.Encode))
	pref, err := xmlLenTypeToMoovPrefix(strings.TrimSpace(xf.LenType), enc)
	if err != nil {
		return nil, err
	}

	tagLen := 0
	if strings.TrimSpace(xf.TagTypeLen) != "" {
		tagLen, _ = strconv.Atoi(strings.TrimSpace(xf.TagTypeLen))
	}
	tagType := strings.TrimSpace(xf.TagType)
	tagEnc := xmlEncodeToMoov(tagType)
	if tagEnc == encoding.Binary && strings.EqualFold(tagType, "ASCII") {
		tagEnc = encoding.ASCII
	}
	isBerTLVTag := strings.EqualFold(tagType, "BerTLVTag")
	if isBerTLVTag {
		tagEnc = encoding.BerTLVTag
		tagLen = 0
	}
	if tagLen <= 0 && !isBerTLVTag {
		tagLen = 3
	}

	sub := make(map[string]field.Field, len(xf.Fields))
	for _, sf := range xf.Fields {
		key := strings.TrimSpace(sf.Tag)
		if key == "" {
			key = strings.TrimSpace(sf.Name)
		}
		if key == "" {
			continue
		}
		ff, err := makeFieldFromXML(sf)
		if err != nil {
			return nil, fmt.Errorf("subfield %q: %w", key, err)
		}
		if isBerTLVTag {
			// Для BER-TLV длина каждого тега кодируется по BER правилам (prefix.BerTLV),
			// поэтому фиксированные/LLL префиксы из XML для subfield здесь не применимы.
			ff = wrapFieldWithPrefix(ff, prefix.BerTLV)
		}
		sub[key] = ff
	}
	tagSort := isoSort.StringsByInt
	if isBerTLVTag {
		tagSort = isoSort.StringsByHex
	}
	return field.NewComposite(&field.Spec{
		Length:      length,
		Description: strings.TrimSpace(xf.Desc),
		Pref:        pref,
		Tag: &field.TagSpec{
			Length: tagLen,
			Enc:    tagEnc,
			Sort:   tagSort,
		},
		Subfields: sub,
	}), nil
}

func wrapFieldWithPrefix(f field.Field, p prefix.Prefixer) field.Field {
	spec := f.Spec()
	if spec == nil {
		return f
	}
	copied := *spec
	copied.Pref = p
	switch f.(type) {
	case *field.String:
		return field.NewString(&copied)
	case *field.Numeric:
		return field.NewNumeric(&copied)
	case *field.Binary:
		return field.NewBinary(&copied)
	case *field.Hex:
		return field.NewHex(&copied)
	case *field.Composite:
		return field.NewComposite(&copied)
	default:
		return f
	}
}

func xmlEncodeToMoov(enc string) encoding.Encoder {
	switch strings.ToUpper(strings.TrimSpace(enc)) {
	case "BCH", "BCD":
		return encoding.Binary
	case "ASCII", "":
		return encoding.ASCII
	default:
		return encoding.ASCII
	}
}

func xmlLenTypeToMoovPrefix(lenType string, enc encoding.Encoder) (prefix.Prefixer, error) {
	var p prefix.Prefixers
	if enc == encoding.Binary {
		p = prefix.Binary
	} else {
		p = prefix.ASCII
	}
	switch strings.ToUpper(strings.TrimSpace(lenType)) {
	case "FIX", "":
		return p.Fixed, nil
	case "LVAR":
		return p.L, nil
	case "LLVAR":
		return p.LL, nil
	case "LLLVAR":
		return p.LLL, nil
	case "LLLLVAR":
		return p.LLLL, nil
	default:
		return nil, fmt.Errorf("unsupported lenType %q", lenType)
	}
}

func buildPayloadFromISO8583(step scenario.Step, vars map[string]string) ([]byte, *iso8583lib.MessageSpec, error) {
	sp, err := resolveISO8583Spec(step, vars)
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
	stdsort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	for _, row := range rows {
		val, err := expandStepPlaceholders(interpolate(vars, row.tpl))
		if err != nil {
			return nil, nil, fmt.Errorf("tcp_iso8583_fields[%d]: %w", row.id, err)
		}
		if strings.HasPrefix(strings.TrimSpace(val), "{") {
			var obj map[string]any
			if err := json.Unmarshal([]byte(val), &obj); err == nil && len(obj) > 0 {
				for k, v := range obj {
					path := fmt.Sprintf("%d.%s", row.id, strings.TrimSpace(k))
					if err := marshalISO8583PathValue(msg, path, v); err != nil {
						return nil, nil, fmt.Errorf("iso8583 field %d path %s: %w", row.id, path, err)
					}
				}
				continue
			}
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

func marshalISO8583PathValue(msg *iso8583lib.Message, path string, v any) error {
	if err := msg.MarshalPath(path, v); err != nil {
		s, ok := v.(string)
		if !ok {
			return err
		}
		trimmed := strings.TrimSpace(s)
		if !isOddLengthHex(trimmed) || !strings.Contains(strings.ToLower(err.Error()), "odd length hex string") {
			return err
		}
		// Частый случай EMV-тегов: значение приходит как n3 (например 643),
		// а hex-поле ожидает четное число символов (0643).
		return msg.MarshalPath(path, "0"+trimmed)
	}
	return nil
}

func isOddLengthHex(s string) bool {
	if len(s) == 0 || len(s)%2 == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
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
	if strings.TrimSpace(step.TCPISO8583SpecXML) != "" {
		return resolveISO8583Spec(step, nil)
	}
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
