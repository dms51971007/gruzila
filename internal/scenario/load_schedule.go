package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LoadSchedule — суточное расписание нагрузки по реальному времени.
//
// max_load — верхняя граница TPS (100% нагрузки).
// intervals — карта «час начала (0–23) → процент от max_load» до следующего ключа; границы только по целым часам (10 = 10:00).
// До первого ключа суток действует хвост после последней отметки (переход через полночь).
//
// В сценарии можно задать inline-блок load_schedule или путь:
//
//	load_schedule_profile: "includes/load-schedule-sbp.yml"
//
// Пример содержимого include-файла (корень документа — те же поля, без вложенного load_schedule:):
//
//	max_load: 100
//	timezone: Europe/Moscow
//	intervals:
//	  0: 10
//	  2: 15
//	  10: 50
//	  13: 25
type LoadSchedule struct {
	MaxLoad  float64 `yaml:"max_load" json:"max_load"`
	Timezone string  `yaml:"timezone,omitempty" json:"timezone,omitempty"`

	intervalsRaw map[string]float64 `yaml:"-" json:"-"`
	sorted       []loadSchedPoint   `yaml:"-" json:"-"`
	loc          *time.Location     `yaml:"-" json:"-"`
}

type loadSchedPoint struct {
	startMin int
	percent  float64
}

// UnmarshalYAML поддерживает ключи intervals как строки и как числа (0: 10, 2: 15).
func (ls *LoadSchedule) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("load_schedule: expected mapping")
	}
	ls.MaxLoad = 0
	ls.Timezone = ""
	ls.intervalsRaw = nil
	var intervalsNode *yaml.Node
	for i := 0; i < len(n.Content); i += 2 {
		if i+1 >= len(n.Content) {
			break
		}
		key := strings.TrimSpace(n.Content[i].Value)
		valNode := n.Content[i+1]
		switch key {
		case "max_load":
			if err := valNode.Decode(&ls.MaxLoad); err != nil {
				return fmt.Errorf("load_schedule.max_load: %w", err)
			}
		case "timezone":
			if err := valNode.Decode(&ls.Timezone); err != nil {
				return fmt.Errorf("load_schedule.timezone: %w", err)
			}
		case "intervals":
			intervalsNode = valNode
		default:
			return fmt.Errorf("load_schedule: unknown key %q", key)
		}
	}
	if intervalsNode != nil {
		m, err := decodeIntervalsYAMLMap(intervalsNode)
		if err != nil {
			return err
		}
		ls.intervalsRaw = m
	}
	return nil
}

func decodeIntervalsYAMLMap(n *yaml.Node) (map[string]float64, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("load_schedule.intervals: expected mapping")
	}
	out := make(map[string]float64)
	for i := 0; i < len(n.Content); i += 2 {
		if i+1 >= len(n.Content) {
			break
		}
		keyNode := n.Content[i]
		valNode := n.Content[i+1]
		keyStr, err := yamlScalarKeyToString(keyNode)
		if err != nil {
			return nil, fmt.Errorf("load_schedule.intervals key: %w", err)
		}
		var pct float64
		if err := valNode.Decode(&pct); err != nil {
			return nil, fmt.Errorf("load_schedule.intervals[%s]: %w", keyStr, err)
		}
		out[keyStr] = pct
	}
	return out, nil
}

func yamlScalarKeyToString(n *yaml.Node) (string, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		return strings.TrimSpace(n.Value), nil
	default:
		return "", fmt.Errorf("expected scalar key, got kind %v", n.Kind)
	}
}

// Compile разбирает интервалы и часовой пояс. Вызывается из Validate.
func (ls *LoadSchedule) Compile() error {
	if ls == nil {
		return nil
	}
	if len(ls.intervalsRaw) == 0 {
		ls.sorted = nil
		ls.loc = nil
		return nil
	}
	if ls.MaxLoad <= 0 {
		return fmt.Errorf("load_schedule.max_load must be > 0 when intervals are set")
	}
	tz := strings.TrimSpace(ls.Timezone)
	if tz == "" {
		ls.loc = time.Local
	} else {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return fmt.Errorf("load_schedule.timezone %q: %w", tz, err)
		}
		ls.loc = loc
	}
	type kv struct {
		min int
		pct float64
	}
	tmp := make([]kv, 0, len(ls.intervalsRaw))
	seen := make(map[int]struct{})
	for key, pct := range ls.intervalsRaw {
		m, err := parseIntervalTimeKey(key)
		if err != nil {
			return fmt.Errorf("load_schedule.intervals key %q: %w", key, err)
		}
		if _, ok := seen[m]; ok {
			return fmt.Errorf("load_schedule.intervals: duplicate start time %s", formatMinAsClock(m))
		}
		seen[m] = struct{}{}
		if pct < 0 {
			return fmt.Errorf("load_schedule.intervals[%s]: percent must be >= 0", key)
		}
		tmp = append(tmp, kv{min: m, pct: pct})
	}
	sort.Slice(tmp, func(i, j int) bool { return tmp[i].min < tmp[j].min })
	ls.sorted = make([]loadSchedPoint, len(tmp))
	for i, e := range tmp {
		ls.sorted[i] = loadSchedPoint{startMin: e.min, percent: e.pct}
	}
	return nil
}

func formatMinAsClock(m int) string {
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// HasSchedule — заданы max_load и непустые интервалы (после Compile).
func (ls *LoadSchedule) HasSchedule() bool {
	return ls != nil && len(ls.sorted) > 0 && ls.MaxLoad > 0 && ls.loc != nil
}

// BriefSummary — одна строка для UI: max_load, таймзона, границы интервалов (час → % от max_load).
func (ls *LoadSchedule) BriefSummary() string {
	if ls == nil || !ls.HasSchedule() {
		return ""
	}
	tz := strings.TrimSpace(ls.Timezone)
	if tz == "" {
		tz = "Local"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "max %g, %s — ", ls.MaxLoad, tz)
	for i, p := range ls.sorted {
		if i > 0 {
			b.WriteString(" · ")
		}
		if p.startMin%60 == 0 {
			fmt.Fprintf(&b, "%dh→%.0f%%", p.startMin/60, p.percent)
		} else {
			fmt.Fprintf(&b, "%s→%.0f%%", formatMinAsClock(p.startMin), p.percent)
		}
	}
	return b.String()
}

// ScheduledBaseTPS возвращает целевой TPS до ramp-up: max_load * (интервальный%) / 100 * (cfg.Percent/100).
// cfgPercent — RunConfig.Percent с UI (глобальный «газ»); при 0 трактуем как 100.
func (ls *LoadSchedule) ScheduledBaseTPS(wall time.Time, cfgPercent int) float64 {
	if !ls.HasSchedule() {
		return 0
	}
	pct := ls.intervalPercentAt(wall.In(ls.loc))
	throttle := float64(cfgPercent)
	if throttle <= 0 {
		throttle = 100
	}
	return ls.MaxLoad * (pct / 100.0) * (throttle / 100.0)
}

func (ls *LoadSchedule) intervalPercentAt(t time.Time) float64 {
	m := t.Hour()*60 + t.Minute()
	return ls.intervalPercentForMinute(m)
}

func (ls *LoadSchedule) intervalPercentForMinute(m int) float64 {
	p := ls.sorted
	n := len(p)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return p[0].percent
	}
	// Индекс последнего старта <= m
	k := -1
	for i := n - 1; i >= 0; i-- {
		if p[i].startMin <= m {
			k = i
			break
		}
	}
	if k < 0 {
		// m раньше первой отметки — хвост после последней отметки до полуночи
		return p[n-1].percent
	}
	return p[k].percent
}

func parseIntervalTimeKey(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty time")
	}
	if strings.Contains(s, ":") {
		return 0, fmt.Errorf("use hour 0-23 only, not %q (minutes like 10:30 are not supported)", s)
	}
	h, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid hour %q: %w", s, err)
	}
	if h < 0 || h > 23 {
		return 0, fmt.Errorf("hour out of range [0,23]: %d", h)
	}
	return h * 60, nil
}

// applyLoadScheduleProfile подгружает load_schedule из файла по пути load_schedule_profile (относительно сценария).
func applyLoadScheduleProfile(scenarioPath string, sc *Scenario) error {
	rel := strings.TrimSpace(sc.LoadScheduleProfile)
	if rel == "" {
		return nil
	}
	if sc.LoadSchedule != nil {
		return fmt.Errorf("scenario: use either load_schedule or load_schedule_profile, not both")
	}
	baseDir := filepath.Dir(scenarioPath)
	fpath := filepath.Clean(filepath.Join(baseDir, rel))
	data, err := os.ReadFile(fpath)
	if err != nil {
		return fmt.Errorf("load_schedule_profile %q: %w", rel, err)
	}
	var ls LoadSchedule
	if err := yaml.Unmarshal(data, &ls); err != nil {
		return fmt.Errorf("parse load_schedule_profile %q: %w", fpath, err)
	}
	sc.LoadSchedule = &ls
	return nil
}
