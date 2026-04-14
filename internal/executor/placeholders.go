package executor

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reStepNow        = regexp.MustCompile(`\{\{__now:([^}]*)\}\}`)
	reStepRandDigits = regexp.MustCompile(`\{\{__randDigits:(\d+)\}\}`)
	reStepRandHex    = regexp.MustCompile(`\{\{__randHex:(\d+)\}\}`)
	reStepPAN        = regexp.MustCompile(`\{\{__pan:(\d+)(?::([0-9]{1,18}))?\}\}`)
)

// expandStepPlaceholders подставляет в строку генераторы до отправки по TCP/в hex.
//
//   - {{__now:LAYOUT}} — time.Now().Format(LAYOUT); пустой LAYOUT → "0102150405" (как поле 7 MDhhmmss)
//   - {{__randDigits:N}} — N десятичных цифр (crypto/rand)
//   - {{__randHex:N}} — N символов hex (0123456789abcdef)
//   - {{__pan:N}} — валидный по Luhn PAN длины N (N=12..19)
//   - {{__pan:N:BIN}} — PAN длины N c указанным BIN/префиксом
//
// Выполняется после interpolate(vars, …). Не пересекается с {{var}} пользователя.
func expandStepPlaceholders(s string) (string, error) {
	var err error
	for reStepNow.MatchString(s) {
		loc := reStepNow.FindStringSubmatchIndex(s)
		if loc == nil || len(loc) < 4 {
			break
		}
		layout := strings.TrimSpace(s[loc[2]:loc[3]])
		if layout == "" {
			layout = "0102150405"
		}
		repl := time.Now().In(time.Local).Format(layout)
		s = s[:loc[0]] + repl + s[loc[1]:]
	}
	s, err = expandRandDigits(s)
	if err != nil {
		return "", err
	}
	s, err = expandRandHex(s)
	if err != nil {
		return "", err
	}
	s, err = expandPAN(s)
	if err != nil {
		return "", err
	}
	return s, nil
}

func expandRandDigits(s string) (string, error) {
	for {
		loc := reStepRandDigits.FindStringSubmatchIndex(s)
		if loc == nil || len(loc) < 4 {
			return s, nil
		}
		n, err := strconv.Atoi(s[loc[2]:loc[3]])
		if err != nil || n < 1 || n > 256 {
			return "", fmt.Errorf("__randDigits: invalid length %q", s[loc[2]:loc[3]])
		}
		buf := make([]byte, n)
		for i := range buf {
			v, err := rand.Int(rand.Reader, big.NewInt(10))
			if err != nil {
				return "", fmt.Errorf("__randDigits: %w", err)
			}
			buf[i] = byte('0' + v.Int64())
		}
		s = s[:loc[0]] + string(buf) + s[loc[1]:]
	}
}

func expandRandHex(s string) (string, error) {
	const digits = "0123456789abcdef"
	for {
		loc := reStepRandHex.FindStringSubmatchIndex(s)
		if loc == nil || len(loc) < 4 {
			return s, nil
		}
		n, err := strconv.Atoi(s[loc[2]:loc[3]])
		if err != nil || n < 1 || n > 512 {
			return "", fmt.Errorf("__randHex: invalid length %q", s[loc[2]:loc[3]])
		}
		var b strings.Builder
		b.Grow(n)
		for i := 0; i < n; i++ {
			v, err := rand.Int(rand.Reader, big.NewInt(16))
			if err != nil {
				return "", fmt.Errorf("__randHex: %w", err)
			}
			b.WriteByte(digits[v.Int64()])
		}
		s = s[:loc[0]] + b.String() + s[loc[1]:]
	}
}

func expandPAN(s string) (string, error) {
	for {
		loc := reStepPAN.FindStringSubmatchIndex(s)
		if loc == nil || len(loc) < 6 {
			return s, nil
		}
		rawLen := s[loc[2]:loc[3]]
		n, err := strconv.Atoi(rawLen)
		if err != nil || n < 12 || n > 19 {
			return "", fmt.Errorf("__pan: invalid length %q (expected 12..19)", rawLen)
		}
		bin := ""
		if loc[4] >= 0 && loc[5] >= 0 {
			bin = s[loc[4]:loc[5]]
		}
		pan, err := generateLuhnPAN(n, bin)
		if err != nil {
			return "", err
		}
		s = s[:loc[0]] + pan + s[loc[1]:]
	}
}

func generateLuhnPAN(totalLen int, bin string) (string, error) {
	if len(bin) > totalLen-1 {
		return "", fmt.Errorf("__pan: bin length %d is too long for pan length %d", len(bin), totalLen)
	}
	bodyLen := totalLen - 1
	var b strings.Builder
	b.Grow(totalLen)
	b.WriteString(bin)
	for b.Len() < bodyLen {
		v, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("__pan: %w", err)
		}
		b.WriteByte(byte('0' + v.Int64()))
	}
	body := b.String()
	check := luhnCheckDigit(body)
	return body + string(rune('0'+check)), nil
}

func luhnCheckDigit(numberWithoutCheck string) int {
	sum := 0
	double := true
	for i := len(numberWithoutCheck) - 1; i >= 0; i-- {
		d := int(numberWithoutCheck[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return (10 - (sum % 10)) % 10
}
