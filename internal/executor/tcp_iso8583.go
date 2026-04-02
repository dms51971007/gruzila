package executor

import (
	"fmt"
	"strconv"
	"strings"

	iso8583lib "github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/specs"

	"gruzilla/internal/scenario"
)

func iso8583MessageSpecByName(name string) (*iso8583lib.MessageSpec, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "spec87ascii":
		return specs.Spec87ASCII, nil
	case "spec87hex":
		return specs.Spec87Hex, nil
	case "spec87track2":
		return specs.Spec87Track2, nil
	default:
		return nil, fmt.Errorf("unknown tcp_iso8583_spec %q (spec87ascii, spec87hex, spec87track2)", name)
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
	for key, tpl := range step.TCPISO8583Fields {
		fid, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || fid < 0 {
			return nil, nil, fmt.Errorf("tcp_iso8583_fields: invalid field id %q", key)
		}
		val, err := expandStepPlaceholders(interpolate(vars, tpl))
		if err != nil {
			return nil, nil, fmt.Errorf("tcp_iso8583_fields[%d]: %w", fid, err)
		}
		if err := msg.Field(fid, val); err != nil {
			return nil, nil, fmt.Errorf("iso8583 field %d: %w", fid, err)
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
		vars[name] = s
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
		if got != want {
			return fmt.Errorf("tcp_iso8583_assert field %d: got %q want %q", fid, got, want)
		}
	}
	return nil
}
