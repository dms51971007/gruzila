package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gruzilla/internal/executor"
)

func main() {
	// scenario — путь к YAML, который executor держит как активный сценарий.
	scenarioPath := flag.String("scenario", "", "path to scenario YAML file")
	// addr — HTTP-адрес API executor (CLI обращается к нему через --executor-url).
	addr := flag.String("addr", ":8081", "listen address, e.g. :8081")
	// logFile — путь к файлу логов executor. Если пусто, только stdout.
	logFile := flag.String("log-file", "", "path to executor log file (optional)")
	flag.Parse()

	if *scenarioPath == "" {
		log.Fatal("missing --scenario")
	}

	logCloser, err := setupLogging(*logFile)
	if err != nil {
		log.Fatalf("setup logging: %v", err)
	}
	if logCloser != nil {
		defer logCloser.Close()
	}

	svc, err := executor.NewService(*scenarioPath)
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

func setupLogging(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	// Important: when executor is started via backend/CLI in JSON mode,
	// stdout/stderr may be detached or piped to a closed writer.
	// Writing directly to file keeps logging reliable in that mode.
	log.SetOutput(f)
	log.Printf("executor logging to file: %s", path)
	return f, nil
}
