package executor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gruzilla/internal/scenario"
	"gruzilla/internal/templates"

	"github.com/segmentio/kafka-go"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var ErrNotRunning = errors.New("executor is not running")

type RunConfig struct {
	Percent       int               `json:"percent"`
	BaseTPS       float64           `json:"base_tps"`
	RampUpSeconds int               `json:"ramp_up_seconds,omitempty"`
	Variables     map[string]string `json:"variables,omitempty"`
}

type Metrics struct {
	AttemptsCount int64   `json:"attempts_count"` // всего запущено попыток (success + error + in-flight)
	SuccessCount  int64   `json:"success_count"`
	ErrorCount    int64   `json:"error_count"`
	LastLatency   int64   `json:"last_latency_ms"`
	TargetTPS     float64 `json:"target_tps"`  // к какому TPS стремимся (effectiveTPS)
	CurrentTPS    float64 `json:"current_tps"` // сколько реально попыток запустили за последнюю секунду
}

type Status struct {
	Running      bool       `json:"running"`
	ScenarioPath string     `json:"scenario_path"`
	ScenarioName string     `json:"scenario_name"`
	Config       RunConfig  `json:"config"`
	Metrics      Metrics    `json:"metrics"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
}

type Service struct {
	mu            sync.RWMutex
	status        Status
	active        scenario.Scenario
	stopCh        chan struct{}
	running       bool
	attemptsCount atomic.Int64
	successCount  atomic.Int64
	errorCount    atomic.Int64
	lastLatency   atomic.Int64
	lastAttempts  atomic.Int64
	prom          *PrometheusMetrics
}

func NewService(scenarioPath string) (*Service, error) {
	sc, err := scenario.LoadFromFile(scenarioPath)
	if err != nil {
		return nil, fmt.Errorf("load scenario: %w", err)
	}
	return &Service{
		status: Status{
			ScenarioPath: scenarioPath,
			ScenarioName: sc.Name,
			Config: RunConfig{
				Percent: 100,
				BaseTPS: 1,
			},
		},
		active: sc,
		prom:   InitPrometheusMetrics(scenarioPath),
	}, nil
}

func (s *Service) Start(cfg RunConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return errors.New("executor already running")
	}
	if cfg.Percent <= 0 {
		cfg.Percent = 100
	}
	if cfg.BaseTPS <= 0 {
		cfg.BaseTPS = 1
	}
	now := time.Now().UTC()
	s.status.Config = cfg
	s.status.Running = true
	s.status.StartedAt = &now
	s.stopCh = make(chan struct{})
	s.running = true
	s.attemptsCount.Store(0)
	s.lastAttempts.Store(0)
	s.successCount.Store(0)
	s.errorCount.Store(0)
	s.lastLatency.Store(0)
	go s.runLoop(s.stopCh)
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return ErrNotRunning
	}
	ch := s.stopCh
	s.running = false
	s.status.Running = false
	s.mu.Unlock()
	close(ch)
	if s.prom != nil {
		s.prom.Update(
			s.attemptsCount.Load(),
			s.successCount.Load(),
			s.errorCount.Load(),
			0, 0,
			s.lastLatency.Load(),
			false,
		)
	}
	return nil
}

func (s *Service) Update(cfg RunConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return ErrNotRunning
	}
	if cfg.Percent > 0 {
		s.status.Config.Percent = cfg.Percent
	}
	if cfg.BaseTPS > 0 {
		s.status.Config.BaseTPS = cfg.BaseTPS
	}
	if cfg.RampUpSeconds > 0 {
		s.status.Config.RampUpSeconds = cfg.RampUpSeconds
	}
	return nil
}

func (s *Service) Status() Status {
	s.mu.RLock()
	st := s.status
	s.mu.RUnlock()
	st.Metrics.AttemptsCount = s.attemptsCount.Load()
	st.Metrics.SuccessCount = s.successCount.Load()
	st.Metrics.ErrorCount = s.errorCount.Load()
	st.Metrics.LastLatency = s.lastLatency.Load()
	return st
}

func (s *Service) Metrics() Metrics {
	s.mu.RLock()
	m := s.status.Metrics
	s.mu.RUnlock()
	m.AttemptsCount = s.attemptsCount.Load()
	m.SuccessCount = s.successCount.Load()
	m.ErrorCount = s.errorCount.Load()
	m.LastLatency = s.lastLatency.Load()
	return m
}

// ResetMetrics обнуляет счётчики и last_error. Разрешено только когда нагрузка остановлена (running == false).
func (s *Service) ResetMetrics() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return errors.New("cannot reset metrics while load is running; stop first")
	}
	s.attemptsCount.Store(0)
	s.successCount.Store(0)
	s.errorCount.Store(0)
	s.lastLatency.Store(0)
	s.status.LastError = ""
	s.status.StartedAt = nil
	s.status.Metrics.TargetTPS = 0
	s.status.Metrics.CurrentTPS = 0
	return nil
}

// Reload перечитывает YAML сценария с диска и обновляет активный сценарий,
// не останавливая текущий прогон.
func (s *Service) Reload() error {
	s.mu.RLock()
	path := s.status.ScenarioPath
	s.mu.RUnlock()

	sc, err := scenario.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("reload scenario: %w", err)
	}

	s.mu.Lock()
	s.active = sc
	s.status.ScenarioName = sc.Name
	// Сбрасывать или нет метрики/ошибки — решение на уровне продукта.
	// Здесь только очищаем last_error, чтобы новые ошибки относились к новому сценарию.
	s.status.LastError = ""
	s.mu.Unlock()

	return nil
}

func (s *Service) runLoop(stop <-chan struct{}) {
	r := newRunner(s.buildVariables)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			cfg, startedAt := s.currentConfig()
			targetTPS := effectiveTPS(cfg, startedAt)

			iterations := int(math.Round(targetTPS))
			if targetTPS > 0 && iterations == 0 {
				iterations = 1
			}

			var wg sync.WaitGroup
			for i := 0; i < iterations; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					s.attemptsCount.Add(1)
					started := time.Now()
					var err error
					func() {
						defer func() {
							if p := recover(); p != nil {
								err = fmt.Errorf("panic: %v", p)
							}
						}()
						err = r.executeScenario(s.active)
					}()
					latency := time.Since(started).Milliseconds()
					s.lastLatency.Store(latency)

					if err != nil {
						s.errorCount.Add(1)
						s.mu.Lock()
						s.status.LastError = err.Error()
						s.mu.Unlock()
						return
					}
					s.successCount.Add(1)
				}()
			}
			wg.Wait()

			// Реальный TPS считаем как прирост attempts_count за секунду.
			currentAttempts := s.attemptsCount.Load()
			prevAttempts := s.lastAttempts.Swap(currentAttempts)
			currentTPS := float64(currentAttempts - prevAttempts)

			s.mu.Lock()
			s.status.Metrics.TargetTPS = targetTPS
			s.status.Metrics.CurrentTPS = currentTPS
			s.mu.Unlock()

			if s.prom != nil {
				s.prom.Update(
					s.attemptsCount.Load(),
					s.successCount.Load(),
					s.errorCount.Load(),
					currentTPS,
					targetTPS,
					s.lastLatency.Load(),
					true,
				)
			}
		}
	}
}

func (s *Service) currentConfig() (RunConfig, *time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.Config, s.status.StartedAt
}

func effectiveTPS(cfg RunConfig, startedAt *time.Time) float64 {
	base := cfg.BaseTPS * float64(cfg.Percent) / 100.0
	if cfg.RampUpSeconds <= 0 || startedAt == nil {
		return base
	}
	elapsed := time.Since(*startedAt).Seconds()
	total := float64(cfg.RampUpSeconds)
	if total <= 0 {
		return base
	}
	progress := elapsed / total
	if progress <= 0 {
		return 0
	}
	if progress >= 1 {
		return base
	}
	return base * progress
}

type runner struct {
	httpClient  *http.Client
	buildVars   func() map[string]string
	kafkaMu     sync.Mutex
	kafkaWriter map[string]*kafka.Writer
	dbMu        sync.Mutex
	dbPool      map[string]*sql.DB
}

func newRunner(buildVars func() map[string]string) *runner {
	return &runner{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		buildVars:   buildVars,
		kafkaWriter: make(map[string]*kafka.Writer),
		dbPool:      make(map[string]*sql.DB),
	}
}

func (r *runner) executeScenario(sc scenario.Scenario) error {
	for _, step := range sc.Steps {
		if err := r.executeStep(step); err != nil {
			name := step.Name
			if name == "" {
				name = step.Type
			}
			return fmt.Errorf("step %q failed: %w", name, err)
		}
	}
	return nil
}

func (r *runner) executeStep(step scenario.Step) error {
	switch step.Type {
	case "rest":
		return r.executeREST(step)
	case "kafka":
		return r.executeKafka(step)
	case "db":
		return r.executeDB(step)
	case "mq":
		return errors.New("mq step is not implemented yet")
	default:
		return fmt.Errorf("unsupported step type: %s", step.Type)
	}
}

func (r *runner) executeREST(step scenario.Step) error {
	vars := r.buildVars()
	method := strings.TrimSpace(step.Method)
	if method == "" {
		method = http.MethodPost
	}
	url := interpolate(vars, step.URL)
	bodyStr, err := r.bodyFromStep(step, vars)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(method, url, strings.NewReader(bodyStr))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if bodyStr != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range step.Headers {
		req.Header.Set(interpolate(vars, k), interpolate(vars, v))
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if expected, ok := getExpectedStatus(step.Assert); ok && resp.StatusCode != expected {
		return fmt.Errorf("unexpected status: got %d want %d", resp.StatusCode, expected)
	}
	return nil
}

func (r *runner) executeKafka(step scenario.Step) error {
	vars := r.buildVars()
	topic := interpolate(vars, step.Topic)
	if topic == "" {
		return errors.New("kafka topic is empty")
	}
	if len(step.Brokers) == 0 {
		return errors.New("kafka brokers list is empty")
	}
	brokers := make([]string, 0, len(step.Brokers))
	for _, b := range step.Brokers {
		brokers = append(brokers, interpolate(vars, b))
	}

	w, err := r.kafkaWriterFor(brokers, topic)
	if err != nil {
		return err
	}
	value, err := r.bodyFromStep(step, vars)
	if err != nil {
		return err
	}

	msg := kafka.Message{
		Key:   []byte(interpolate(vars, step.Key)),
		Value: []byte(value),
		Time:  time.Now().UTC(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("write kafka message: %w", err)
	}
	return nil
}

func (r *runner) executeDB(step scenario.Step) error {
	vars := r.buildVars()
	dsn := interpolate(vars, step.DBDSN)
	if dsn == "" {
		return errors.New("db_dsn is empty")
	}
	query := interpolate(vars, step.DBQuery)

	db, err := r.dbFor(dsn)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("execute db query: %w", err)
	}
	defer rows.Close()

	if expected, ok := getExpectedRows(step.Assert); ok {
		var count int
		for rows.Next() {
			count++
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read db rows: %w", err)
		}
		if count != expected {
			return fmt.Errorf("unexpected rows count: got %d want %d", count, expected)
		}
		return nil
	}

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read db rows: %w", err)
		}
		return errors.New("db query returned no rows")
	}
	return nil
}

func (r *runner) bodyFromStep(step scenario.Step, vars map[string]string) (string, error) {
	// If template name provided, render from templates/ using full var map.
	if step.Template != "" {
		data := make(map[string]any, len(vars))
		for k, v := range vars {
			data[k] = v
		}
		out, err := templates.Render(step.Template, data)
		if err != nil {
			return "", err
		}
		return out, nil
	}
	// Fallback to raw body with {{var}} interpolation.
	return interpolate(vars, step.Body), nil
}

func (r *runner) kafkaWriterFor(brokers []string, topic string) (*kafka.Writer, error) {
	if len(brokers) == 0 {
		return nil, errors.New("kafka brokers list is empty")
	}
	cacheKey := strings.Join(brokers, ",") + "|" + topic

	r.kafkaMu.Lock()
	defer r.kafkaMu.Unlock()
	if w, ok := r.kafkaWriter[cacheKey]; ok {
		return w, nil
	}

	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.LeastBytes{},
		RequiredAcks: kafka.RequireOne,
		Async:        false,
	}
	r.kafkaWriter[cacheKey] = w
	return w, nil
}

func (r *runner) dbFor(dsn string) (*sql.DB, error) {
	r.dbMu.Lock()
	defer r.dbMu.Unlock()
	if db, ok := r.dbPool[dsn]; ok {
		return db, nil
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	if r.dbPool == nil {
		r.dbPool = make(map[string]*sql.DB)
	}
	r.dbPool[dsn] = db
	return db, nil
}

func interpolate(vars map[string]string, src string) string {
	out := src
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

func getExpectedStatus(assert map[string]any) (int, bool) {
	if assert == nil {
		return 0, false
	}
	raw, ok := assert["status"]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func getExpectedRows(assert map[string]any) (int, bool) {
	if assert == nil {
		return 0, false
	}
	raw, ok := assert["rows"]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func (s *Service) buildVariables() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vars := make(map[string]string, len(s.status.Config.Variables)+2)
	for k, v := range s.status.Config.Variables {
		vars[k] = v
	}
	vars["scenarioPath"] = s.status.ScenarioPath
	vars["scenarioName"] = s.status.ScenarioName
	return vars
}

