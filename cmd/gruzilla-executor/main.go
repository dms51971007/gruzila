package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"time"

	"gruzilla/internal/executor"
)

func main() {
	// scenario — путь к YAML, который executor держит как активный сценарий.
	scenarioPath := flag.String("scenario", "", "path to scenario YAML file")
	// addr — HTTP-адрес API executor (CLI обращается к нему через --executor-url).
	addr := flag.String("addr", ":8081", "listen address, e.g. :8081")
	flag.Parse()

	if *scenarioPath == "" {
		log.Fatal("missing --scenario")
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
