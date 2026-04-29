package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
	"gruzilla/internal/executor"
)

const (
	defaultLogRotateMaxSizeMB  = 50
	defaultLogRotateMaxBackups = 5
	defaultLogRotateMaxAgeDays = 14
)

type logRotateConfig struct {
	maxSizeMB  int
	maxBackups int
	maxAgeDays int
}

func main() {
	// scenario — путь к YAML, который executor держит как активный сценарий.
	scenarioPath := flag.String("scenario", "", "path to scenario YAML file")
	// addr — HTTP-адрес API executor (CLI обращается к нему через --executor-url).
	addr := flag.String("addr", ":8081", "listen address, e.g. :8081")
	// logFile — путь к файлу логов executor. Если пусто, только stdout.
	logFile := flag.String("log-file", "", "path to executor log file (optional)")
	logMaxSizeMB := flag.Int("log-max-size-mb", defaultLogRotateMaxSizeMB, "max size in megabytes before rotating log file")
	logMaxBackups := flag.Int("log-max-backups", defaultLogRotateMaxBackups, "max number of old rotated log files to keep")
	logMaxAgeDays := flag.Int("log-max-age-days", defaultLogRotateMaxAgeDays, "max age in days to retain old rotated log files")
	flag.Parse()

	if *scenarioPath == "" {
		log.Fatal("missing --scenario")
	}

	logCloser, err := setupLogging(*logFile, logRotateConfig{
		maxSizeMB:  *logMaxSizeMB,
		maxBackups: *logMaxBackups,
		maxAgeDays: *logMaxAgeDays,
	})
	if err != nil {
		log.Fatalf("setup logging: %v", err)
	}
	if logCloser != nil {
		defer logCloser.Close()
	}

	svc, err := executor.NewService(*scenarioPath, strings.TrimSpace(*logFile) != "")
	if err != nil {
		log.Fatalf("init service: %v", err)
	}

	handler := executor.NewHandler(svc)
	mux := http.NewServeMux()
	handler.Register(mux)

	server := &http.Server{Addr: *addr, Handler: mux}
	// Shutdown endpoint запускает graceful stop HTTP-сервера,
	// чтобы executors restart мог корректно перезапустить процесс.
	handler.SetShutdownFunc(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	})

	log.Printf("gruzilla-executor listening on %s, scenario=%s", *addr, *scenarioPath)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server stopped: %v", err)
	}
}

func setupLogging(path string, cfg logRotateConfig) (io.Closer, error) {
	if path == "" {
		return nil, nil
	}
	if cfg.maxSizeMB <= 0 {
		cfg.maxSizeMB = defaultLogRotateMaxSizeMB
	}
	if cfg.maxBackups < 0 {
		cfg.maxBackups = defaultLogRotateMaxBackups
	}
	if cfg.maxAgeDays < 0 {
		cfg.maxAgeDays = defaultLogRotateMaxAgeDays
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	rotator := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    cfg.maxSizeMB,
		MaxBackups: cfg.maxBackups,
		MaxAge:     cfg.maxAgeDays,
		LocalTime:  true,
		Compress:   false,
	}
	// Important: when executor is started via backend/CLI in JSON mode,
	// stdout/stderr may be detached or piped to a closed writer.
	// Writing directly to file keeps logging reliable in that mode.
	log.SetOutput(rotator)
	log.Printf("executor logging to file (rotation enabled): %s max_size_mb=%d max_backups=%d max_age_days=%d",
		path, cfg.maxSizeMB, cfg.maxBackups, cfg.maxAgeDays)
	return rotator, nil
}
