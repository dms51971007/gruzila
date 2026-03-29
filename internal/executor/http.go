package executor

import (
	"encoding/json"
	"net/http"

	"gruzilla/internal/api"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ShutdownFunc вызывается при POST /api/v1/shutdown для корректного завершения процесса (например, http.Server.Shutdown).
type ShutdownFunc func()

type Handler struct {
	svc          *Service
	shutdownFunc ShutdownFunc
}

// NewHandler создаёт HTTP-обработчик API поверх Service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// SetShutdownFunc задаёт функцию, вызываемую при запросе shutdown (в main передаётся server.Shutdown).
func (h *Handler) SetShutdownFunc(f ShutdownFunc) {
	h.shutdownFunc = f
}

// Register монтирует все API endpoint'ы executor в переданный mux.
// Бизнес-методы API намеренно принимают только POST, чтобы избежать
// случайного изменения состояния через браузерный GET.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/start", h.onlyPOST(h.start))
	mux.HandleFunc("/api/v1/stop", h.onlyPOST(h.stop))
	mux.HandleFunc("/api/v1/update", h.onlyPOST(h.update))
	mux.HandleFunc("/api/v1/status", h.onlyPOST(h.status))
	mux.HandleFunc("/api/v1/metrics", h.onlyPOST(h.metrics))
	mux.HandleFunc("/api/v1/reload", h.onlyPOST(h.reload))
	mux.HandleFunc("/api/v1/reset_metrics", h.onlyPOST(h.resetMetrics))
	mux.HandleFunc("/api/v1/shutdown", h.onlyPOST(h.shutdown))
	// Prometheus scrape endpoint (GET, no auth)
	mux.Handle("/metrics", promhttp.Handler())
}

func (h *Handler) onlyPOST(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			api.WriteError(w, "only POST allowed")
			return
		}
		next(w, r)
	}
}

// start запускает runLoop с параметрами из JSON тела запроса.
func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	var cfg RunConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		api.WriteError(w, "invalid JSON body")
		return
	}
	if err := h.svc.Start(cfg); err != nil {
		api.WriteError(w, err.Error())
		return
	}
	api.WriteSuccess(w, h.svc.Status())
}

// stop останавливает активную нагрузку.
func (h *Handler) stop(w http.ResponseWriter, _ *http.Request) {
	if err := h.svc.Stop(); err != nil {
		api.WriteError(w, err.Error())
		return
	}
	api.WriteSuccess(w, h.svc.Status())
}

// update меняет текущую конфигурацию нагрузки без рестарта runLoop.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var cfg RunConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		api.WriteError(w, "invalid JSON body")
		return
	}
	if err := h.svc.Update(cfg); err != nil {
		api.WriteError(w, err.Error())
		return
	}
	api.WriteSuccess(w, h.svc.Status())
}

// status возвращает полный runtime-статус executor.
func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	api.WriteSuccess(w, h.svc.Status())
}

// metrics возвращает "плоский" срез числовых метрик для CLI/интеграций.
func (h *Handler) metrics(w http.ResponseWriter, _ *http.Request) {
	api.WriteSuccess(w, h.svc.Metrics())
}

// reload перечитывает YAML сценария с диска без перезапуска процесса.
func (h *Handler) reload(w http.ResponseWriter, _ *http.Request) {
	if err := h.svc.Reload(); err != nil {
		api.WriteError(w, err.Error())
		return
	}
	api.WriteSuccess(w, h.svc.Status())
}

// resetMetrics очищает counters и last_error (в т.ч. при активной нагрузке).
func (h *Handler) resetMetrics(w http.ResponseWriter, _ *http.Request) {
	if err := h.svc.ResetMetrics(); err != nil {
		api.WriteError(w, err.Error())
		return
	}
	api.WriteSuccess(w, h.svc.Status())
}

// shutdown инициирует асинхронный graceful stop HTTP-сервера.
// Ответ отправляется до фактического завершения процесса.
func (h *Handler) shutdown(w http.ResponseWriter, _ *http.Request) {
	if h.shutdownFunc == nil {
		api.WriteError(w, "shutdown not configured")
		return
	}
	api.WriteSuccess(w, map[string]string{"message": "shutting down"})
	go h.shutdownFunc()
}
