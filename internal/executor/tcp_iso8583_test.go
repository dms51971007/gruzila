package executor

import (
	"testing"

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
