package scenario

import (
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadScheduleYAMLNumericKeys(t *testing.T) {
	const doc = `
max_load: 100
timezone: UTC
intervals:
  0: 10
  2: 15
  10: 50
  13: 25
`
	var ls LoadSchedule
	if err := yaml.Unmarshal([]byte(doc), &ls); err != nil {
		t.Fatal(err)
	}
	if err := ls.Compile(); err != nil {
		t.Fatal(err)
	}
	if !ls.HasSchedule() || ls.MaxLoad != 100 {
		t.Fatalf("compile: %+v", ls)
	}
	// 12:00 UTC → [10:00,13:00) → 50%
	w := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	if g := ls.ScheduledBaseTPS(w, 100); g != 50 {
		t.Fatalf("noon want 50 got %v", g)
	}
	// 01:00 → [0,2) → 10%
	w = time.Date(2026, 4, 6, 1, 0, 0, 0, time.UTC)
	if g := ls.ScheduledBaseTPS(w, 100); g != 10 {
		t.Fatalf("01:00 want 10 got %v", g)
	}
	// 15:00 → [13:00, next 0:00) → 25%
	w = time.Date(2026, 4, 6, 15, 0, 0, 0, time.UTC)
	if g := ls.ScheduledBaseTPS(w, 100); g != 25 {
		t.Fatalf("15:00 want 25 got %v", g)
	}
	want := "max 100, UTC — 0h→10% · 2h→15% · 10h→50% · 13h→25%"
	if g := ls.BriefSummary(); g != want {
		t.Fatalf("BriefSummary: got %q want %q", g, want)
	}
}

func TestLoadScheduleThrottlePercent(t *testing.T) {
	ls := LoadSchedule{MaxLoad: 100, Timezone: "UTC", intervalsRaw: map[string]float64{"0": 80}}
	if err := ls.Compile(); err != nil {
		t.Fatal(err)
	}
	w := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if g := ls.ScheduledBaseTPS(w, 50); g != 40 {
		t.Fatalf("80%% of 100 at 50%% throttle = 40, got %v", g)
	}
}

func TestLoadScheduleCompileErrors(t *testing.T) {
	ls := LoadSchedule{MaxLoad: 0, intervalsRaw: map[string]float64{"0": 10}}
	if err := ls.Compile(); err == nil {
		t.Fatal("want max_load error")
	}
	ls = LoadSchedule{MaxLoad: 10, intervalsRaw: map[string]float64{"0": 10, "00": 20}}
	if err := ls.Compile(); err == nil {
		t.Fatal("want duplicate key")
	}
	ls = LoadSchedule{MaxLoad: 10, Timezone: "Nowhere/Invalid", intervalsRaw: map[string]float64{"0": 1}}
	if err := ls.Compile(); err == nil {
		t.Fatal("want tz error")
	}
}

func TestParseIntervalTimeKey(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"2", 120},
		{"10", 600},
		{"23", 23 * 60},
	} {
		m, err := parseIntervalTimeKey(tc.in)
		if err != nil || m != tc.want {
			t.Fatalf("%q: got %d %v want %d", tc.in, m, err, tc.want)
		}
	}
	if _, err := parseIntervalTimeKey("10:30"); err == nil {
		t.Fatal("expected error for HH:MM")
	}
}

func TestLoadFromFile_loadScheduleProfile(t *testing.T) {
	path := filepath.Join("..", "..", "scenarios", "sbp-no-ssl.yml")
	sc, err := LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sc.LoadSchedule == nil || !sc.LoadSchedule.HasSchedule() {
		t.Fatal("expected load schedule from profile")
	}
	if sc.LoadSchedule.MaxLoad != 100 {
		t.Fatalf("max_load %v", sc.LoadSchedule.MaxLoad)
	}
}
