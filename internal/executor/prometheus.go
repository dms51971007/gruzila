package executor

import (
	"github.com/prometheus/client_golang/prometheus"
)

// PrometheusMetrics хранит метрики Prometheus с лейблом scenario для обновления.
type PrometheusMetrics struct {
	attempts    prometheus.Gauge
	success     prometheus.Gauge
	errors      prometheus.Gauge
	currentTPS  prometheus.Gauge
	targetTPS   prometheus.Gauge
	lastLatency prometheus.Gauge
	running     prometheus.Gauge
}

// InitPrometheusMetrics регистрирует метрики для сценария и возвращает объект для обновления.
func InitPrometheusMetrics(scenarioLabel string) *PrometheusMetrics {
	const namespace = "gruzilla"
	labels := prometheus.Labels{"scenario": scenarioLabel}

	attemptsVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "attempts_total",
		Help:      "Total scenario runs started",
	}, []string{"scenario"})
	successVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "success_total",
		Help:      "Total successful scenario runs",
	}, []string{"scenario"})
	errorsVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "errors_total",
		Help:      "Total failed scenario runs",
	}, []string{"scenario"})
	currentTPSVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "current_tps",
		Help:      "Actual TPS in the last tick",
	}, []string{"scenario"})
	targetTPSVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "target_tps",
		Help:      "Target TPS",
	}, []string{"scenario"})
	lastLatencyVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "last_latency_ms",
		Help:      "Last run latency (ms)",
	}, []string{"scenario"})
	runningVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "running",
		Help:      "1 if load running, 0 otherwise",
	}, []string{"scenario"})

	prometheus.MustRegister(attemptsVec, successVec, errorsVec, currentTPSVec, targetTPSVec, lastLatencyVec, runningVec)

	return &PrometheusMetrics{
		attempts:    attemptsVec.With(labels),
		success:     successVec.With(labels),
		errors:      errorsVec.With(labels),
		currentTPS:  currentTPSVec.With(labels),
		targetTPS:   targetTPSVec.With(labels),
		lastLatency: lastLatencyVec.With(labels),
		running:     runningVec.With(labels),
	}
}

// Update обновляет все метрики из текущего состояния.
func (p *PrometheusMetrics) Update(attempts, success, errors int64, currentTPS, targetTPS float64, lastLatencyMs int64, running bool) {
	p.attempts.Set(float64(attempts))
	p.success.Set(float64(success))
	p.errors.Set(float64(errors))
	p.currentTPS.Set(currentTPS)
	p.targetTPS.Set(targetTPS)
	p.lastLatency.Set(float64(lastLatencyMs))
	if running {
		p.running.Set(1)
	} else {
		p.running.Set(0)
	}
}
