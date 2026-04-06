package executor

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	iso8583lib "github.com/moov-io/iso8583"

	"gruzilla/internal/scenario"
)

const (
	tcpDefaultDialMS  = 15000
	tcpDefaultReadMS  = 60000
	tcpDefaultReadCap = 262144
	tcpReadMinRemain  = 256 * time.Millisecond
)

func (r *runner) executeTCP(step scenario.Step, vars map[string]string) error {
	addr := strings.TrimSpace(interpolate(vars, step.TCPAddr))
	if addr == "" {
		return fmt.Errorf("tcp: empty address")
	}

	var payload []byte
	var isoBuildSpec *iso8583lib.MessageSpec
	if len(step.TCPISO8583Fields) > 0 {
		var err error
		payload, isoBuildSpec, err = buildPayloadFromISO8583(step, vars)
		if err != nil {
			return err
		}
	} else {
		rawHex := strings.TrimSpace(interpolate(vars, step.TCPPayloadHex))
		rawText := strings.TrimSpace(interpolate(vars, step.TCPPayload))
		var merged string
		switch {
		case rawHex != "" && rawText != "":
			return fmt.Errorf("tcp: specify only one of tcp_payload_hex or tcp_payload")
		case rawHex != "":
			merged = rawHex
		case rawText != "":
			merged = rawText
		default:
			return fmt.Errorf("tcp: tcp_payload or tcp_payload_hex is empty")
		}

		expanded, err := expandStepPlaceholders(merged)
		if err != nil {
			return fmt.Errorf("tcp placeholders: %w", err)
		}

		if rawHex != "" {
			if enc := strings.TrimSpace(step.TCPPayloadEncoding); enc != "" && strings.ToLower(enc) != "utf8" {
				return fmt.Errorf("tcp_payload_encoding applies only to tcp_payload, not tcp_payload_hex")
			}
			payload, err = decodeFlexibleHex(expanded)
			if err != nil {
				return fmt.Errorf("tcp hex payload: %w", err)
			}
		} else {
			payload, err = encodeTCPPayloadText(expanded, step.TCPPayloadEncoding)
			if err != nil {
				return err
			}
		}
	}

	prefix := strings.TrimSpace(strings.ToLower(interpolate(vars, step.TCPLengthPrefix)))
	frame, err := tcpWrapLengthPrefix(prefix, payload)
	if err != nil {
		return err
	}

	dialMS := step.TCPDialTimeoutMS
	if dialMS <= 0 {
		dialMS = tcpDefaultDialMS
	}
	readMS := step.TCPReadTimeoutMS
	if readMS <= 0 {
		readMS = tcpDefaultReadMS
	}
	readCap := step.TCPReadMaxBytes
	if readCap <= 0 {
		readCap = tcpDefaultReadCap
	}

	dialer := net.Dialer{Timeout: time.Duration(dialMS) * time.Millisecond}
	var conn net.Conn
	if step.TCPTLS {
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			return fmt.Errorf("tcp tls: %w", splitErr)
		}
		sn := strings.TrimSpace(interpolate(vars, step.TCPTLSServerName))
		if sn == "" {
			sn = host
		}
		tlsCfg := &tls.Config{
			ServerName:         sn,
			InsecureSkipVerify: step.TCPTLSInsecure,
			MinVersion:         tls.VersionTLS12,
		}
		if !step.TCPTLSInsecure && sn == "" {
			return fmt.Errorf("tcp tls: set tcp_tls_server_name or use host:port with hostname")
		}
		tconn, err := tls.DialWithDialer(&dialer, "tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("tcp tls dial %s: %w", addr, err)
		}
		conn = tconn
	} else {
		c, err := dialer.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("tcp dial %s: %w", addr, err)
		}
		conn = c
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetWriteDeadline(time.Now().Add(time.Duration(readMS) * time.Millisecond))
	if _, err := conn.Write(frame); err != nil {
		return fmt.Errorf("tcp write: %w", err)
	}
	tcpSrc := fmt.Sprintf("tcp %s", addr)
	r.logTraffic(tcpSrc, "send", hex.EncodeToString(frame))

	resp, err := tcpReadResponse(conn, prefix, readCap, readMS)
	if err != nil {
		return err
	}

	respHex := hex.EncodeToString(resp)
	r.logTraffic(tcpSrc, "recv", respHex)
	return r.tcpHandleResponse(step, vars, resp, respHex, isoBuildSpec)
}

func (r *runner) tcpHandleResponse(step scenario.Step, vars map[string]string, resp []byte, respHex string, isoBuildSpec *iso8583lib.MessageSpec) error {
	if want := strings.TrimSpace(interpolate(vars, step.TCPAssertResponseHex)); want != "" {
		if !strings.Contains(respHex, strings.ToLower(want)) {
			return fmt.Errorf("tcp assert: response hex does not contain %q", want)
		}
	}
	if len(step.TCPExtract) > 0 {
		if err := applyTCPByteExtract(resp, step.TCPExtract, vars); err != nil {
			return err
		}
	}
	unpackSpec, err := tcpResponseISO8583Spec(step, isoBuildSpec)
	if err != nil {
		return err
	}
	if unpackSpec != nil && (len(step.TCPISO8583Extract) > 0 || len(step.TCPISO8583Assert) > 0) {
		if len(resp) == 0 {
			return fmt.Errorf("tcp iso8583: empty response body")
		}
		msg := iso8583lib.NewMessage(unpackSpec)
		if err := msg.Unpack(resp); err != nil {
			pre := resp
			const preMax = 64
			if len(pre) > preMax {
				pre = pre[:preMax]
			}
			return fmt.Errorf("tcp iso8583 unpack: %w (first %d bytes hex=%s)", err, len(pre), hex.EncodeToString(pre))
		}
		if err := applyTCPISO8583ExtractVars(msg, step.TCPISO8583Extract, vars); err != nil {
			return err
		}
		if err := applyTCPISO8583Assert(msg, step.TCPISO8583Assert, vars); err != nil {
			return err
		}
	}
	// Бинарный ISO-ответ не JSON — не вызывать json.Unmarshal, если задан iso8583 extract/assert.
	if stepNeedsJSONExtract(step) && len(step.TCPISO8583Extract) == 0 && len(step.TCPISO8583Assert) == 0 {
		var root any
		if err := json.Unmarshal(resp, &root); err != nil {
			return fmt.Errorf("tcp extract: response is not JSON (%v); use tcp_extract (offset:length) or tcp_iso8583_extract", err)
		}
		if err := applyAllJSONExtracts(step, vars, root); err != nil {
			return fmt.Errorf("tcp extract: %w", err)
		}
	}
	return nil
}

// applyTCPByteExtract кладёт срезы сырого ответа в vars. spec: "offset:length" или "offset:length:hex".
func applyTCPByteExtract(resp []byte, spec map[string]string, vars map[string]string) error {
	for varName, rawTpl := range spec {
		name := strings.TrimSpace(varName)
		if name == "" {
			continue
		}
		s := strings.TrimSpace(interpolate(vars, rawTpl))
		parts := strings.Split(s, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return fmt.Errorf("tcp_extract %q: want \"offset:length\" or \"offset:length:hex\", got %q", name, s)
		}
		off, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || off < 0 {
			return fmt.Errorf("tcp_extract %q: bad offset %q", name, parts[0])
		}
		length, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || length < 0 {
			return fmt.Errorf("tcp_extract %q: bad length %q", name, parts[1])
		}
		asHex := false
		if len(parts) == 3 {
			switch strings.TrimSpace(strings.ToLower(parts[2])) {
			case "hex":
				asHex = true
			default:
				return fmt.Errorf("tcp_extract %q: unknown suffix %q (use hex or omit)", name, parts[2])
			}
		}
		end := off + length
		if end > len(resp) {
			return fmt.Errorf("tcp_extract %q: range [%d:%d] beyond response len %d", name, off, end, len(resp))
		}
		slice := resp[off:end]
		if asHex {
			vars[name] = hex.EncodeToString(slice)
		} else {
			vars[name] = string(slice)
		}
	}
	return nil
}

func tcpWrapLengthPrefix(prefix string, payload []byte) ([]byte, error) {
	switch prefix {
	case "", "none":
		return payload, nil
	case "2be":
		if len(payload) > 65535 {
			return nil, fmt.Errorf("tcp length_prefix 2be: payload %d bytes > 65535", len(payload))
		}
		buf := make([]byte, 2+len(payload))
		binary.BigEndian.PutUint16(buf[:2], uint16(len(payload)))
		copy(buf[2:], payload)
		return buf, nil
	case "4be":
		if len(payload) > 0x7fffffff {
			return nil, fmt.Errorf("tcp length_prefix 4be: payload too large")
		}
		buf := make([]byte, 4+len(payload))
		binary.BigEndian.PutUint32(buf[:4], uint32(len(payload)))
		copy(buf[4:], payload)
		return buf, nil
	case "4ascii":
		n := len(payload)
		if n > 9999 {
			return nil, fmt.Errorf("tcp length_prefix 4ascii: payload %d bytes > 9999", n)
		}
		head := fmt.Sprintf("%04d", n)
		buf := make([]byte, 4+len(payload))
		copy(buf[:4], head)
		copy(buf[4:], payload)
		return buf, nil
	case "6ascii":
		n := len(payload)
		if n > 999999 {
			return nil, fmt.Errorf("tcp length_prefix 6ascii: payload %d bytes > 999999", n)
		}
		head := fmt.Sprintf("%06d", n)
		buf := make([]byte, 6+len(payload))
		copy(buf[:6], head)
		copy(buf[6:], payload)
		return buf, nil
	default:
		return nil, fmt.Errorf("tcp_length_prefix: unknown %q (use \"\", 2be, 4be, 4ascii, 6ascii)", prefix)
	}
}

func tcpReadResponse(conn net.Conn, prefix string, maxBody int, readTimeoutMS int) ([]byte, error) {
	deadline := time.Now().Add(time.Duration(readTimeoutMS) * time.Millisecond)
	var msgLen int
	switch prefix {
	case "", "none":
		b := make([]byte, maxBody+1)
		_ = conn.SetReadDeadline(deadline)
		n, err := conn.Read(b)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("tcp read: %w", err)
		}
		if n > maxBody {
			return nil, fmt.Errorf("tcp read: message exceeds tcp_read_max_bytes (%d)", maxBody)
		}
		return b[:n], nil
	case "2be":
		var hdr [2]byte
		if _, err := tcpReadFull(conn, hdr[:], deadline); err != nil {
			return nil, err
		}
		msgLen = int(binary.BigEndian.Uint16(hdr[:]))
	case "4be":
		var hdr [4]byte
		if _, err := tcpReadFull(conn, hdr[:], deadline); err != nil {
			return nil, err
		}
		msgLen = int(binary.BigEndian.Uint32(hdr[:]))
	case "4ascii":
		var hdr [4]byte
		if _, err := tcpReadFull(conn, hdr[:], deadline); err != nil {
			return nil, err
		}
		ls := strings.TrimSpace(string(hdr[:]))
		parsed, err := strconv.Atoi(ls)
		if err != nil {
			return nil, fmt.Errorf("tcp read 4ascii: invalid length %q: %w", ls, err)
		}
		msgLen = parsed
	case "6ascii":
		var hdr [6]byte
		if _, err := tcpReadFull(conn, hdr[:], deadline); err != nil {
			return nil, err
		}
		ls := strings.TrimSpace(string(hdr[:]))
		parsed, err := strconv.Atoi(ls)
		if err != nil {
			return nil, fmt.Errorf("tcp read 6ascii: invalid length %q: %w", ls, err)
		}
		msgLen = parsed
	default:
		return nil, fmt.Errorf("tcp read: unknown prefix %q", prefix)
	}
	if msgLen < 0 || msgLen > maxBody {
		return nil, fmt.Errorf("tcp read: declared length %d out of range (max %d)", msgLen, maxBody)
	}
	if msgLen == 0 {
		return nil, nil
	}
	body := make([]byte, msgLen)
	if _, err := tcpReadFull(conn, body, deadline); err != nil {
		return nil, err
	}
	return body, nil
}

func tcpReadFull(conn net.Conn, buf []byte, deadline time.Time) (int, error) {
	total := 0
	for total < len(buf) {
		remaining := time.Until(deadline)
		if remaining < tcpReadMinRemain {
			return total, fmt.Errorf("tcp read: deadline exceeded")
		}
		_ = conn.SetReadDeadline(time.Now().Add(remaining))
		n, err := conn.Read(buf[total:])
		if n > 0 {
			total += n
		}
		if err != nil {
			if err == io.EOF {
				if total < len(buf) {
					return total, fmt.Errorf("tcp read: unexpected eof after %d/%d bytes", total, len(buf))
				}
				return total, nil
			}
			return total, fmt.Errorf("tcp read: %w", err)
		}
	}
	return total, nil
}

// encodeTCPPayloadText кодирует строку после плейсхолдеров в байты для tcp_payload.
// По умолчанию — UTF-8 (как []byte в Go). iso8859_1 — строго U+0000..U+00FF на байт.
func encodeTCPPayloadText(s, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf8", "utf-8":
		return []byte(s), nil
	case "iso8859_1", "iso-8859-1", "latin1", "iso_8859_1":
		return stringToISO88591(s)
	default:
		return nil, fmt.Errorf("tcp_payload_encoding: unknown %q (utf8, iso8859_1)", encoding)
	}
}

func stringToISO88591(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			return nil, fmt.Errorf("tcp_payload iso8859_1: rune %U is outside U+0000..U+00FF", r)
		}
		out = append(out, byte(r))
	}
	return out, nil
}

func decodeFlexibleHex(s string) ([]byte, error) {
	clean := strings.TrimSpace(s)
	clean = strings.ReplaceAll(clean, " ", "")
	clean = strings.ReplaceAll(clean, "\n", "")
	clean = strings.ReplaceAll(clean, "\r", "")
	clean = strings.ReplaceAll(clean, "\t", "")
	if len(clean)%2 != 0 {
		return nil, fmt.Errorf("odd hex length %d", len(clean))
	}
	return hex.DecodeString(clean)
}
