package executor

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gruzilla/internal/scenario"
	"gruzilla/internal/templates"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/segmentio/kafka-go"
)

var ErrNotRunning = errors.New("executor is not running")

// RunConfig описывает внешнюю конфигурацию запуска нагрузки.
// Значения приходят через API/CLI и применяются на каждом тике runLoop.
type RunConfig struct {
	Percent       int               `json:"percent"`
	BaseTPS       float64           `json:"base_tps"`
	RampUpSeconds int               `json:"ramp_up_seconds,omitempty"`
	Variables     map[string]string `json:"variables,omitempty"`
}

// Metrics — runtime-метрики текущего прогона.
// AttemptsCount считает запущенные итерации сценария, а не только success.
type Metrics struct {
	AttemptsCount int64   `json:"attempts_count"` // всего запущено попыток (success + error + in-flight)
	SuccessCount  int64   `json:"success_count"`
	ErrorCount    int64   `json:"error_count"`
	LastLatency   int64   `json:"last_latency_ms"`
	AdaptiveTPS   float64 `json:"adaptive_tps"` // динамический ceiling, до которого режем target_tps
	TargetTPS     float64 `json:"target_tps"`   // к какому TPS стремимся (effectiveTPS)
	CurrentTPS    float64 `json:"current_tps"`  // сколько реально попыток запустили за последнюю секунду
}

// Status — полный снимок состояния executor для API /status.
type Status struct {
	Running      bool       `json:"running"`
	ScenarioPath string     `json:"scenario_path"`
	ScenarioName string     `json:"scenario_name"`
	Config       RunConfig  `json:"config"`
	Metrics      Metrics    `json:"metrics"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
}

// Service управляет жизненным циклом сценария, runLoop и счётчиками.
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
	// resetAdaptiveCap сигнализирует runLoop немедленно сбросить adaptive cap
	// после внешнего Update TPS-параметров.
	resetAdaptiveCap atomic.Bool
	prom             *PrometheusMetrics
	// baseVarsSnapshot — неизменяемый снимок Config.Variables + scenarioPath/scenarioName.
	// Пересобирается в Start и Reload; buildVariables отдаёт maps.Clone для итерации (mq мутирует vars).
	baseVarsSnapshot map[string]string
}

// NewService загружает сценарий с диска и инициализирует Service.
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

// Start запускает фоновый runLoop с заданной конфигурацией.
// Повторный вызов при уже активном run возвращает ошибку.
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
	s.refreshBaseVarsSnapshotLocked()
	s.stopCh = make(chan struct{})
	s.running = true
	s.attemptsCount.Store(0)
	s.lastAttempts.Store(0)
	s.successCount.Store(0)
	s.errorCount.Store(0)
	s.lastLatency.Store(0)
	s.resetAdaptiveCap.Store(true)
	go s.runLoop(s.stopCh)
	return nil
}

// Stop останавливает runLoop и фиксирует running=false в статусе.
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

// Update применяет изменение конфигурации "на лету" без остановки runLoop.
func (s *Service) Update(cfg RunConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return ErrNotRunning
	}
	tpsChanged := false
	if cfg.Percent > 0 {
		if s.status.Config.Percent != cfg.Percent {
			s.status.Config.Percent = cfg.Percent
			tpsChanged = true
		}
	}
	if cfg.BaseTPS > 0 {
		if s.status.Config.BaseTPS != cfg.BaseTPS {
			s.status.Config.BaseTPS = cfg.BaseTPS
			tpsChanged = true
		}
	}
	if cfg.RampUpSeconds > 0 {
		if s.status.Config.RampUpSeconds != cfg.RampUpSeconds {
			s.status.Config.RampUpSeconds = cfg.RampUpSeconds
			tpsChanged = true
		}
	}
	if tpsChanged {
		s.status.Metrics.AdaptiveTPS = 0
		s.resetAdaptiveCap.Store(true)
	}
	// Если позже добавят обновление cfg.Variables через Update — вызвать refreshBaseVarsSnapshotLocked().
	return nil
}

// Status возвращает агрегированный статус с актуальными атомарными счётчиками.
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

// Metrics возвращает срез метрик без служебных полей Status.
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
	s.status.Metrics.AdaptiveTPS = 0
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
	s.refreshBaseVarsSnapshotLocked()
	s.mu.Unlock()

	return nil
}

// scenarioMaxConcurrent — максимум сценариев «в полёте» (очередь + выполнение),
// как раньше у inflightLimiter; ёмкость семафора и jobCh.
const scenarioMaxConcurrent = 4096

// scenarioWorkerCount — число долгоживущих воркеров (не создаём go на каждый тик).
func scenarioWorkerCount() int {
	n := runtime.GOMAXPROCS(0) * 2
	if n < 4 {
		n = 4
	}
	if n > 1024 {
		n = 1024
	}
	return n
}

// runScenarioIteration выполняет одну попытку сценария (счётчики, panic recover, last_error).
func (s *Service) runScenarioIteration(r *runner) {
	transactionNumber := s.attemptsCount.Add(1)
	started := time.Now()
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("panic: %v", p)
			}
		}()
		err = r.executeScenario(s.active, transactionNumber)
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
}

func (s *Service) scenarioWorker(r *runner, jobCh <-chan struct{}, sem chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for range jobCh {
		s.runScenarioIteration(r)
		<-sem
	}
}

// runLoop — основной цикл планирования нагрузки (тик раз в секунду).
// На каждом тике:
// 1) рассчитывает desired/target TPS;
// 2) ставит задачи в очередь воркерам (без go на каждую итерацию);
// 3) пересчитывает current TPS и adaptive cap;
// 4) обновляет status и Prometheus-метрики.
func (s *Service) runLoop(stop <-chan struct{}) {
	r := newRunner(s.buildVariables)

	// sem: слот занят с постановки в очередь до завершения воркером (как старый inflightLimiter).
	sem := make(chan struct{}, scenarioMaxConcurrent)
	jobCh := make(chan struct{}, scenarioMaxConcurrent)
	var workerWG sync.WaitGroup
	for w := 0; w < scenarioWorkerCount(); w++ {
		workerWG.Add(1)
		go s.scenarioWorker(r, jobCh, sem, &workerWG)
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer func() {
		close(jobCh)
		workerWG.Wait()
	}()

	// Интервал для TPS — между началами обработки соседних тиков (wall clock), иначе
	// длительная работа тика смещает окно и «ломает» скорость/адаптацию.
	var lastTPSSampleAt time.Time
	// carryIterations хранит дробный "остаток" iterations между тиками,
	// чтобы корректно поддерживать targetTPS < 1 и не терять точность.
	carryIterations := 0.0
	adaptiveCapTPS := 0.0

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// recover: паника в тике иначе убьёт runLoop — метрики (current_tps) перестанут обновляться.
			func() {
				defer func() {
					if p := recover(); p != nil {
						s.mu.Lock()
						s.status.LastError = fmt.Sprintf("runLoop tick panic: %v", p)
						s.mu.Unlock()
					}
				}()

				now := time.Now()
				var elapsedSec float64
				if lastTPSSampleAt.IsZero() {
					lastTPSSampleAt = now
					elapsedSec = 1.0
				} else {
					elapsedSec = now.Sub(lastTPSSampleAt).Seconds()
					lastTPSSampleAt = now
				}
				if elapsedSec <= 0 {
					elapsedSec = 1e-6
				}

				cfg, startedAt := s.currentConfig()
				desiredTPS := effectiveTPS(cfg, startedAt)
				if math.IsNaN(desiredTPS) || math.IsInf(desiredTPS, 0) {
					desiredTPS = 0
				}
				if s.resetAdaptiveCap.Swap(false) {
					adaptiveCapTPS = desiredTPS
					// При резком изменении TPS не переносим старый дробный "хвост".
					carryIterations = 0
				}
				if adaptiveCapTPS <= 0 || math.IsNaN(adaptiveCapTPS) || math.IsInf(adaptiveCapTPS, 0) {
					adaptiveCapTPS = desiredTPS
				}
				// Effective target is capped by what the system has recently sustained.
				targetTPS := desiredTPS
				if adaptiveCapTPS > 0 && targetTPS > adaptiveCapTPS {
					targetTPS = adaptiveCapTPS
				}

				// Планируем количество запусков по фактическому временному окну.
				// Это исправляет искажение TPS на дробных значениях (например 0.2, 0.5, 1.7).
				planned := targetTPS*elapsedSec + carryIterations
				if planned < 0 {
					planned = 0
				}
				iterations := int(math.Floor(planned))
				carryIterations = planned - float64(iterations)

				for i := 0; i < iterations; i++ {
					select {
					case sem <- struct{}{}:
						select {
						case jobCh <- struct{}{}:
						default:
							<-sem
						}
					default:
						// Saturated: skip this slot for current cycle.
						continue
					}
				}

				// Реальный TPS: прирост attempts_count за интервал между тиками.
				currentAttempts := s.attemptsCount.Load()
				prevAttempts := s.lastAttempts.Swap(currentAttempts)
				currentTPS := float64(currentAttempts-prevAttempts) / elapsedSec

				// Adapt cap with bounded decay/recovery.
				// Important: never drop cap to a single noisy sample instantly.
				if currentTPS < targetTPS*0.95 {
					decayed := adaptiveCapTPS * 0.85 // max 15% drop per cycle
					if currentTPS > 0 && decayed < currentTPS {
						decayed = currentTPS
					}
					if decayed < 1 {
						decayed = 1
					}
					adaptiveCapTPS = decayed
				} else {
					// recover faster than before to avoid being stuck too low
					grow := adaptiveCapTPS*1.2 + 1
					if grow < 1 {
						grow = 1
					}
					adaptiveCapTPS = math.Min(desiredTPS, grow)
				}

				s.mu.Lock()
				s.status.Metrics.AdaptiveTPS = adaptiveCapTPS
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
			}()
		}
	}
}

// currentConfig безопасно читает текущий RunConfig и startedAt.
func (s *Service) currentConfig() (RunConfig, *time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.Config, s.status.StartedAt
}

// effectiveTPS вычисляет целевой TPS с учётом percent и ramp-up.
// При ramp-up TPS растёт линейно от 0 до base*percent/100.
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

// runner исполняет одну итерацию сценария и держит локальные кэши клиентов.
type runner struct {
	httpClient  *http.Client
	buildVars   func() map[string]string
	kafkaMu     sync.Mutex
	kafkaWriter map[string]*kafka.Writer
	dbMu        sync.Mutex
	dbPool      map[string]*sql.DB
}

// newRunner создаёт runner с переиспользуемыми HTTP/Kafka/DB-клиентами.
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

// executeScenario выполняет все шаги сценария по порядку.
// requestId всегда генерируется как UUID для каждой итерации и общий
// для всех шагов этой итерации. TransactionNumber — номер попытки.
func (r *runner) executeScenario(sc scenario.Scenario, transactionNumber int64) error {
	vars := r.buildVars()
	vars["requestId"] = newUUIDString()
	vars["TransactionNumber"] = strconv.FormatInt(transactionNumber, 10)

	for _, step := range sc.Steps {
		if err := r.executeStep(step, vars); err != nil {
			name := step.Name
			if name == "" {
				name = step.Type
			}
			return fmt.Errorf("step %q failed: %w", name, err)
		}
	}
	return nil
}

func newUUIDString() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		seq := atomic.AddUint64(&uuidFallbackSeq, 1)
		return fmt.Sprintf("req-%d-%d", time.Now().UTC().UnixNano(), seq)
	}
	// UUIDv4 bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]),
		uint16(b[4])<<8|uint16(b[5]),
		uint16(b[6])<<8|uint16(b[7]),
		uint16(b[8])<<8|uint16(b[9]),
		uint64(b[10])<<40|uint64(b[11])<<32|uint64(b[12])<<24|uint64(b[13])<<16|uint64(b[14])<<8|uint64(b[15]),
	)
}

var uuidFallbackSeq uint64

// executeStep маршрутизирует выполнение в обработчик соответствующего типа.
func (r *runner) executeStep(step scenario.Step, vars map[string]string) error {
	switch step.Type {
	case "rest":
		return r.executeREST(step, vars)
	case "kafka":
		return r.executeKafka(step, vars)
	case "db":
		return r.executeDB(step, vars)
	case "mq":
		return r.executeMQ(step, vars)
	default:
		return fmt.Errorf("unsupported step type: %s", step.Type)
	}
}

// executeREST выполняет HTTP-запрос шага и проверяет assert.status при наличии.
func (r *runner) executeREST(step scenario.Step, vars map[string]string) error {
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

// executeKafka отправляет одно сообщение в Kafka-топик шага.
func (r *runner) executeKafka(step scenario.Step, vars map[string]string) error {
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

// executeDB выполняет SQL-запрос и проверяет assert.rows (если задан).
func (r *runner) executeDB(step scenario.Step, vars map[string]string) error {
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

// bodyFromStep строит payload шага: сначала template (если задан),
// иначе raw body с интерполяцией {{var}}.
func (r *runner) bodyFromStep(step scenario.Step, vars map[string]string) (string, error) {
	// If template name provided, render from templates/ using full var map.
	if step.Template != "" {
		// map[string]string совместим с {{ .requestId }} в text/template; лишняя map[string]any не нужна.
		out, err := templates.Render(step.Template, vars)
		if err != nil {
			return "", err
		}
		return out, nil
	}
	// Fallback to raw body with {{var}} interpolation.
	return interpolate(vars, step.Body), nil
}

// executeMQ выполняет mq put/get через Artemis STOMP factory.
// Для get поддерживает циклическое чтение до timeout, пока не найдётся
// сообщение, удовлетворяющее assert.
func (r *runner) executeMQ(step scenario.Step, vars map[string]string) error {
	queue := interpolate(vars, step.Queue)
	if queue == "" {
		return errors.New("mq queue is empty")
	}

	connName := interpolate(vars, step.MQConnName)
	channel := interpolate(vars, step.MQChannel)
	qm := interpolate(vars, step.MQQueueMgr)
	user := interpolate(vars, step.MQUser)
	password := interpolate(vars, step.MQPassword)
	action := strings.ToLower(strings.TrimSpace(step.MQAction))
	if action == "" {
		action = "put"
	}

	if connName == "" {
		return errors.New("mq_conn_name is required for mq step")
	}

	// ActiveMQ Artemis via STOMP (mq_artemis.go)
	cf := mqConnectionFactory{
		ConnName: connName,
		Channel:  channel,
		QueueMgr: qm,
		AppUser:  user,
		AppPass:  password,
	}

	switch action {
	case "put":
		bodyStr, err := r.bodyFromStep(step, vars)
		if err != nil {
			return err
		}
		if bodyStr == "" {
			return errors.New("mq put body is empty")
		}
		// headers: общие из YAML (как для rest) + mq_headers; при конфликте ключей побеждает mq_headers.
		headers := interpolateStringMap(mqHeaderSource(step), vars)
		// Сохраняем вычисленные headers в vars текущей итерации:
		// это позволяет шагу mq.get использовать те же значения (например RequestId)
		// для broker-side selector.
		for k, v := range headers {
			if strings.TrimSpace(k) == "" {
				continue
			}
			vars[k] = v
		}
		return cf.Put(queue, bodyStr, headers)
	case "get":
		resolvedAssert := interpolateAssert(step.Assert, vars)
		waitMS := step.MQWaitMS
		if waitMS <= 0 {
			waitMS = 15000
		}
		selector := buildArtemisSelector(interpolate(vars, step.MQSelector), vars)
		timeout := time.Duration(waitMS) * time.Millisecond
		deadline := time.Now().Add(timeout)
		var lastMismatch string

		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				if lastMismatch != "" {
					return fmt.Errorf("mq get: timeout waiting matching message; last mismatch: %s", lastMismatch)
				}
				return fmt.Errorf("mq get: no message within %v", timeout)
			}

			msg, _, err := cf.Get(queue, remaining, selector)
			if err != nil {
				if strings.Contains(err.Error(), "no message within") {
					if lastMismatch != "" {
						return fmt.Errorf("mq get: timeout waiting matching message; last mismatch: %s", lastMismatch)
					}
				}
				return err
			}
			if msg == "" {
				continue
			}

			if len(resolvedAssert) == 0 {
				return nil
			}

			var payload map[string]any
			if err := json.Unmarshal([]byte(msg), &payload); err != nil {
				lastMismatch = fmt.Sprintf("invalid JSON payload: %v", err)
				continue
			}

			// success: если поле в payload отсутствует — вероятно «чужое» сообщение
			// (грязная очередь, пограничные сообщения брокера) — пропускаем и читаем дальше.
			// Если поле есть, но не совпало с ожиданием — явный ответ сервиса (например success=false).
			if ok, reason := checkMQSuccessAssert(payload, resolvedAssert); !ok {
				if mqSuccessAssertFieldPresent(payload, resolvedAssert) {
					return fmt.Errorf("mq assert failed: %s", reason)
				}
				lastMismatch = reason
				continue
			}

			ok, reason := matchesMQAssert(payload, resolvedAssert)
			if ok {
				return nil
			}
			lastMismatch = reason
		}
	default:
		return fmt.Errorf("unsupported mq_action: %s", step.MQAction)
	}
}

// buildArtemisSelector готовит broker-side selector для SUBSCRIBE.
// Если в mq_selector уже передано выражение (есть '='), используем как есть.
// Иначе считаем, что это имя header-поля и строим выражение field = 'requestId'.
func buildArtemisSelector(rawSelector string, vars map[string]string) string {
	s := strings.TrimSpace(rawSelector)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "=") {
		return s
	}
	selectorValue := selectorValueFromVars(s, vars)
	if selectorValue == "" {
		return ""
	}
	escaped := strings.ReplaceAll(selectorValue, "'", "''")
	return fmt.Sprintf("%s = '%s'", s, escaped)
}

func selectorValueFromVars(selectorField string, vars map[string]string) string {
	if len(vars) == 0 {
		return ""
	}
	if v, ok := vars[selectorField]; ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	needle := strings.ToLower(strings.TrimSpace(selectorField))
	for k, v := range vars {
		if strings.ToLower(strings.TrimSpace(k)) == needle && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if v := strings.TrimSpace(vars["requestId"]); v != "" {
		return v
	}
	return ""
}

// matchesMQAssert сверяет текстовые поля корреляции (requestId/clientGuid).
// Предусмотрены разные варианты регистра ключа RequestID в payload.
func matchesMQAssert(payload map[string]any, assert map[string]any) (bool, string) {
	// requestId / clientGuid точное сравнение строк
	if wantReqID, ok := assert["requestId"].(string); ok && wantReqID != "" {
		gotRaw, ok2 := payload["RequestID"]
		if !ok2 {
			gotRaw, ok2 = payload["requestID"]
		}
		if !ok2 {
			gotRaw, ok2 = payload["requestId"]
		}
		got, ok3 := gotRaw.(string)
		if !ok3 || got != wantReqID {
			return false, fmt.Sprintf("RequestID=%q, want %q", got, wantReqID)
		}
	}
	if wantClient, ok := assert["clientGuid"].(string); ok && wantClient != "" {
		got, ok2 := payload["clientGuid"].(string)
		if !ok2 || got != wantClient {
			return false, fmt.Sprintf("clientGuid=%q, want %q", got, wantClient)
		}
	}

	return true, ""
}

// checkMQSuccessAssert отдельно валидирует success-поле и даёт fast-fail.
// Это позволяет не ждать timeout, если получили "неуспешный" ответ.
func checkMQSuccessAssert(payload map[string]any, assert map[string]any) (bool, string) {
	if assert == nil {
		return true, ""
	}
	successExpected, hasSuccess := assert["success"]
	if !hasSuccess {
		return true, ""
	}
	successFieldName := "success"
	if v, ok := assert["success_field"].(string); ok && v != "" {
		successFieldName = v
	}
	got, ok := payload[successFieldName]
	if !ok || !jsonEqual(got, successExpected) {
		return false, fmt.Sprintf("%s=%v, want %v", successFieldName, got, successExpected)
	}
	return true, ""
}

// mqSuccessAssertFieldPresent — в payload реально есть ключ из success_field/success в assert.
// Нужно отличить «нет поля» (skip) от «поле есть, значение неверное» (hard fail).
func mqSuccessAssertFieldPresent(payload map[string]any, assert map[string]any) bool {
	if assert == nil {
		return false
	}
	if _, has := assert["success"]; !has {
		return false
	}
	successFieldName := "success"
	if v, ok := assert["success_field"].(string); ok && v != "" {
		successFieldName = v
	}
	_, has := payload[successFieldName]
	return has
}

// buildMQSelectorFromAssert строит broker-selector из assert-полей.
// Сохранено для совместимости, даже если selector может быть отключён на клиенте.
func buildMQSelectorFromAssert(assert map[string]any) string {
	if assert == nil {
		return ""
	}
	var parts []string
	if wantClient, ok := assert["clientGuid"].(string); ok && wantClient != "" {
		parts = append(parts, "clientGuid = '"+escapeSelectorString(wantClient)+"'")
	}
	if wantReqID, ok := assert["requestId"].(string); ok && wantReqID != "" {
		escaped := escapeSelectorString(wantReqID)
		parts = append(parts, "(requestId = '"+escaped+"' OR requestID = '"+escaped+"' OR RequestID = '"+escaped+"')")
	}
	return strings.Join(parts, " AND ")
}

// escapeSelectorString экранирует одинарные кавычки для STOMP selector.
func escapeSelectorString(v string) string {
	return strings.ReplaceAll(v, "'", "''")
}

// interpolateAssert применяет {{var}}-интерполяцию только к строковым полям assert.
func interpolateAssert(assert map[string]any, vars map[string]string) map[string]any {
	if assert == nil {
		return nil
	}
	out := make(map[string]any, len(assert))
	for k, v := range assert {
		switch vv := v.(type) {
		case string:
			out[k] = interpolate(vars, vv)
		default:
			out[k] = v
		}
	}
	return out
}

// jsonEqual сравнивает значения assert с учётом базовой типовой нормализации.
func jsonEqual(a, b any) bool {
	switch av := a.(type) {
	case bool:
		if bv, ok := b.(bool); ok {
			return av == bv
		}
	case float64:
		switch bv := b.(type) {
		case float64:
			return av == bv
		case int:
			return av == float64(bv)
		}
	default:
		return fmt.Sprint(a) == fmt.Sprint(b)
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// kafkaWriterFor возвращает кэшированный kafka.Writer по связке brokers+topic.
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

// dbFor возвращает кэшированное подключение к БД по DSN.
// При первом создании сразу делает Ping для ранней диагностики.
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

// interpolate подставляет значения variables в шаблоны вида {{key}} и {key}.
// Используется strings.NewReplacer: один проход по строке; порядок замен — сначала более длинные
// шаблоны {{key}}, затем {key}, и при нескольких ключах — более длинные имена первыми,
// чтобы не ломать вложенные совпадения (например ключ "ab" vs "a").
func interpolate(vars map[string]string, src string) string {
	if src == "" || len(vars) == 0 {
		return src
	}
	type pair struct {
		old, new string
	}
	reps := make([]pair, 0, len(vars)*2)
	for k, v := range vars {
		reps = append(reps, pair{"{{" + k + "}}", v})
		reps = append(reps, pair{"{" + k + "}", v})
	}
	sort.Slice(reps, func(i, j int) bool {
		if len(reps[i].old) != len(reps[j].old) {
			return len(reps[i].old) > len(reps[j].old)
		}
		return reps[i].old < reps[j].old
	})
	pairs := make([]string, 0, len(reps)*2)
	for _, r := range reps {
		pairs = append(pairs, r.old, r.new)
	}
	return strings.NewReplacer(pairs...).Replace(src)
}

// mqHeaderSource объединяет step.headers и step.mq_headers для STOMP SEND.
// Раньше учитывались только mq_headers — новые ключи из headers: в YAML терялись.
func mqHeaderSource(step scenario.Step) map[string]string {
	if len(step.Headers) == 0 && len(step.MQHeaders) == 0 {
		return nil
	}
	out := make(map[string]string, len(step.Headers)+len(step.MQHeaders))
	for k, v := range step.Headers {
		out[k] = v
	}
	for k, v := range step.MQHeaders {
		out[k] = v
	}
	return out
}

func interpolateStringMap(src map[string]string, vars map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		key := strings.TrimSpace(interpolate(vars, k))
		if key == "" {
			continue
		}
		out[key] = interpolate(vars, v)
	}
	return out
}

// getExpectedStatus извлекает assert.status в int с допуском разных типов YAML.
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

// getExpectedRows извлекает assert.rows в int с допуском разных типов YAML.
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

// refreshBaseVarsSnapshotLocked обновляет снимок переменных сценария (без requestId / TransactionNumber).
// Должно вызываться под s.mu (write lock).
func (s *Service) refreshBaseVarsSnapshotLocked() {
	n := len(s.status.Config.Variables) + 2
	if n < 2 {
		n = 2
	}
	m := make(map[string]string, n)
	for k, v := range s.status.Config.Variables {
		m[k] = v
	}
	m["scenarioPath"] = s.status.ScenarioPath
	m["scenarioName"] = s.status.ScenarioName
	s.baseVarsSnapshot = m
}

// buildVariables формирует базовый набор переменных итерации из Config.Variables
// и служебных полей сценария.
func (s *Service) buildVariables() map[string]string {
	s.mu.RLock()
	snap := s.baseVarsSnapshot
	s.mu.RUnlock()
	if snap == nil {
		s.mu.Lock()
		if s.baseVarsSnapshot == nil {
			s.refreshBaseVarsSnapshotLocked()
		}
		snap = s.baseVarsSnapshot
		s.mu.Unlock()
	}
	// Копия: шаги mq записывают вычисленные заголовки обратно в vars.
	return maps.Clone(snap)
}
