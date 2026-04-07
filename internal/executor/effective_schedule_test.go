package executor

import (
	"testing"
	"time"

	"gruzilla/internal/scenario"

	"gopkg.in/yaml.v3"
)

func TestEffectiveTPSForScenario_noSchedule(t *testing.T) {
	sc := scenario.Scenario{}
	cfg := RunConfig{BaseTPS: 10, Percent: 50, RampUpSeconds: 0}
	started := time.Now().Add(-time.Minute)
	got := effectiveTPSForScenario(sc, cfg, &started, time.Now())
	if got != 5 {
		t.Fatalf("got %v want 5", got)
	}
}

func TestEffectiveTPSForScenario_fromYAML(t *testing.T) {
	const yml = `
name: t
load_schedule:
  max_load: 100
  timezone: UTC
  intervals:
    0: 10
    10: 100
steps:
  - type: rest
    url: "http://127.0.0.1:9/"
`
	var sc scenario.Scenario
	if err := yaml.Unmarshal([]byte(yml), &sc); err != nil {
		t.Fatal(err)
	}
	if err := scenario.Validate(sc); err != nil {
		t.Fatal(err)
	}
	cfg := RunConfig{BaseTPS: 1, Percent: 100, RampUpSeconds: 0}
	started := time.Now().Add(-time.Hour)
	// 05:00 → 10% → 10 TPS
	wall := time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)
	if g := effectiveTPSForScenario(sc, cfg, &started, wall); g != 10 {
		t.Fatalf("05:00 want 10 got %v", g)
	}
	wall = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if g := effectiveTPSForScenario(sc, cfg, &started, wall); g != 100 {
		t.Fatalf("12:00 want 100 got %v", g)
	}
	cfg.IgnoreLoadSchedule = true
	if g := effectiveTPSForScenario(sc, cfg, &started, wall); g != 1 {
		t.Fatalf("with ignore schedule want base_tps 1 got %v", g)
	}
}
