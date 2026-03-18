package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type apiResponse struct {
	Status string `json:"status"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
}

type config struct {
	Addr               string
	CLICommand         string
	CLIArgs            []string
	CLIWorkDir         string
	CLITimeoutSeconds  int
	DefaultExecutorURL string
}

type fileConfig struct {
	Addr string `yaml:"addr"`
	CLI  struct {
		Command            string   `yaml:"command"`
		Args               []string `yaml:"args"`
		ArgsLine           string   `yaml:"args_line"`
		WorkDir            string   `yaml:"workdir"`
		TimeoutSeconds     int      `yaml:"timeout_seconds"`
		DefaultExecutorURL string   `yaml:"default_executor_url"`
	} `yaml:"cli"`
}

type handler struct {
	cfg config
}

const maxLoggedOutput = 4000

func main() {
	configPath := flag.String("config", envOrDefault("GRUZILLA_BACKEND_CONFIG", "config-backend.yml"), "path to backend config YAML")
	flag.Parse()

	cfg := loadConfig(*configPath)
	h := &handler{cfg: cfg}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.health)

	// Run APIs.
	mux.HandleFunc("/api/v1/run/start", h.onlyPOST(h.runStart))
	mux.HandleFunc("/api/v1/run/update", h.onlyPOST(h.runUpdate))
	mux.HandleFunc("/api/v1/run/status", h.onlyPOST(h.runStatus))
	mux.HandleFunc("/api/v1/run/reload", h.onlyPOST(h.runReload))
	mux.HandleFunc("/api/v1/run/reset-metrics", h.onlyPOST(h.runResetMetrics))
	mux.HandleFunc("/api/v1/run/stop", h.onlyPOST(h.runStop))

	// Executors process lifecycle APIs.
	mux.HandleFunc("/api/v1/executors/list", h.onlyPOST(h.executorsList))
	mux.HandleFunc("/api/v1/executors/start", h.onlyPOST(h.executorsStart))
	mux.HandleFunc("/api/v1/executors/stop", h.onlyPOST(h.executorsStop))
	mux.HandleFunc("/api/v1/executors/restart", h.onlyPOST(h.executorsRestart))

	// Scenario CRUD APIs.
	mux.HandleFunc("/api/v1/scenarios/list", h.onlyPOST(h.scenariosList))
	mux.HandleFunc("/api/v1/scenarios/read", h.onlyPOST(h.scenariosRead))
	mux.HandleFunc("/api/v1/scenarios/create", h.onlyPOST(h.scenariosCreate))
	mux.HandleFunc("/api/v1/scenarios/update", h.onlyPOST(h.scenariosUpdate))
	mux.HandleFunc("/api/v1/scenarios/delete", h.onlyPOST(h.scenariosDelete))

	// Template CRUD APIs.
	mux.HandleFunc("/api/v1/templates/list", h.onlyPOST(h.templatesList))
	mux.HandleFunc("/api/v1/templates/read", h.onlyPOST(h.templatesRead))
	mux.HandleFunc("/api/v1/templates/create", h.onlyPOST(h.templatesCreate))
	mux.HandleFunc("/api/v1/templates/update", h.onlyPOST(h.templatesUpdate))
	mux.HandleFunc("/api/v1/templates/delete", h.onlyPOST(h.templatesDelete))

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      h.withCORS(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("gruzilla-backend listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("backend stopped: %v", err)
	}
}

func loadConfig(configPath string) config {
	absConfigPath := configPath
	if absConfigPath == "" {
		absConfigPath = "config-backend.yml"
	}
	if !filepath.IsAbs(absConfigPath) {
		if wd, err := os.Getwd(); err == nil {
			absConfigPath = filepath.Join(wd, absConfigPath)
		}
	}

	// Base defaults.
	cfg := config{
		Addr:               ":8080",
		CLICommand:         "go",
		CLIArgs:            []string{"run", "./cmd/gruzilla-cli"},
		CLIWorkDir:         ".",
		CLITimeoutSeconds:  30,
		DefaultExecutorURL: "http://localhost:8081",
	}

	// Optional config YAML.
	if data, err := os.ReadFile(configPath); err == nil {
		var fc fileConfig
		if err := yaml.Unmarshal(data, &fc); err != nil {
			log.Printf("warn: cannot parse config %q: %v", configPath, err)
		} else {
			applyFileConfig(&cfg, fc)
		}
	} else if !os.IsNotExist(err) {
		log.Printf("warn: cannot read config %q: %v", configPath, err)
	}

	// ENV overrides config file.
	if v := strings.TrimSpace(os.Getenv("GRUZILLA_BACKEND_ADDR")); v != "" {
		cfg.Addr = v
	}
	if v := strings.TrimSpace(os.Getenv("GRUZILLA_CLI_COMMAND")); v != "" {
		cfg.CLICommand = v
	}
	if v := strings.TrimSpace(os.Getenv("GRUZILLA_CLI_ARGS")); v != "" {
		cfg.CLIArgs = strings.Fields(v)
	}
	if v := strings.TrimSpace(os.Getenv("GRUZILLA_CLI_WORKDIR")); v != "" {
		cfg.CLIWorkDir = v
	}
	if v := strings.TrimSpace(os.Getenv("GRUZILLA_CLI_TIMEOUT_SECONDS")); v != "" {
		cfg.CLITimeoutSeconds = parsePositiveInt(v, cfg.CLITimeoutSeconds)
	}
	if v := strings.TrimSpace(os.Getenv("GRUZILLA_DEFAULT_EXECUTOR_URL")); v != "" {
		cfg.DefaultExecutorURL = v
	}
	cfg.CLIWorkDir = resolveCLIWorkDir(cfg.CLIWorkDir, absConfigPath)

	log.Printf("config loaded: addr=%s cli=%s args=%v workdir=%s timeout=%ds default_executor_url=%s",
		cfg.Addr, cfg.CLICommand, cfg.CLIArgs, cfg.CLIWorkDir, cfg.CLITimeoutSeconds, cfg.DefaultExecutorURL)

	return cfg
}

func applyFileConfig(cfg *config, fc fileConfig) {
	if strings.TrimSpace(fc.Addr) != "" {
		cfg.Addr = strings.TrimSpace(fc.Addr)
	}
	if strings.TrimSpace(fc.CLI.Command) != "" {
		cfg.CLICommand = strings.TrimSpace(fc.CLI.Command)
	}
	if len(fc.CLI.Args) > 0 {
		cfg.CLIArgs = fc.CLI.Args
	} else if strings.TrimSpace(fc.CLI.ArgsLine) != "" {
		cfg.CLIArgs = strings.Fields(fc.CLI.ArgsLine)
	}
	if strings.TrimSpace(fc.CLI.WorkDir) != "" {
		cfg.CLIWorkDir = strings.TrimSpace(fc.CLI.WorkDir)
	}
	if fc.CLI.TimeoutSeconds > 0 {
		cfg.CLITimeoutSeconds = fc.CLI.TimeoutSeconds
	}
	if strings.TrimSpace(fc.CLI.DefaultExecutorURL) != "" {
		cfg.DefaultExecutorURL = strings.TrimSpace(fc.CLI.DefaultExecutorURL)
	}
}

func parsePositiveInt(s string, fallback int) int {
	n := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

func resolveCLIWorkDir(rawWorkDir, absConfigPath string) string {
	workDir := strings.TrimSpace(rawWorkDir)
	if workDir == "" {
		workDir = "."
	}

	baseDir := "."
	if absConfigPath != "" {
		baseDir = filepath.Dir(absConfigPath)
	}
	if !filepath.IsAbs(workDir) {
		workDir = filepath.Join(baseDir, workDir)
	}
	workDir = filepath.Clean(workDir)

	// Auto-heal common case: backend started from nested directory.
	if !pathExists(filepath.Join(workDir, "cmd", "gruzilla-cli", "main.go")) {
		parent := filepath.Dir(workDir)
		if parent != workDir && pathExists(filepath.Join(parent, "cmd", "gruzilla-cli", "main.go")) {
			workDir = parent
		}
	}
	return workDir
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func (h *handler) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-Id")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) onlyPOST(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, "only POST allowed")
			return
		}
		next(w, r)
	}
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	writeSuccess(w, map[string]string{"status": "ok"})
}

func requestIDFromHeader(r *http.Request) string {
	id := strings.TrimSpace(r.Header.Get("X-Request-Id"))
	if id != "" {
		return id
	}
	return newUUIDLike()
}

// Lightweight UUIDv4-like string without external dependency.
func newUUIDLike() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type runBody struct {
	ExecutorURL   string            `json:"executor_url"`
	Percent       int               `json:"percent"`
	BaseTPS       float64           `json:"base_tps"`
	RampUpSeconds int               `json:"ramp_up_seconds"`
	Variables     map[string]string `json:"variables"`
}

type crudBody struct {
	Dir      string `json:"dir"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	FromFile string `json:"from_file"`
	Force    bool   `json:"force"`
	Yes      bool   `json:"yes"`
}

type executorsBody struct {
	Scenario    string `json:"scenario"`
	Addr        string `json:"addr"`
	Bin         string `json:"bin"`
	ExecutorURL string `json:"executor_url"`
	PID         int    `json:"pid"`
}

func (h *handler) runStart(w http.ResponseWriter, r *http.Request) {
	var body runBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON body")
		return
	}
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)

	execURL := body.ExecutorURL
	if strings.TrimSpace(execURL) == "" {
		execURL = h.cfg.DefaultExecutorURL
	}
	args := []string{
		"run", "start",
		"--executor-url", execURL,
		"--percent", fmt.Sprintf("%d", body.Percent),
		"--base-tps", fmt.Sprintf("%v", body.BaseTPS),
		"--ramp-up-seconds", fmt.Sprintf("%d", body.RampUpSeconds),
	}
	for k, v := range body.Variables {
		args = append(args, "--var", fmt.Sprintf("%s=%s", k, v))
	}
	h.execCLIAndWrite(w, reqID, args...)
}

func (h *handler) runUpdate(w http.ResponseWriter, r *http.Request) {
	var body runBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON body")
		return
	}
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)

	execURL := body.ExecutorURL
	if strings.TrimSpace(execURL) == "" {
		execURL = h.cfg.DefaultExecutorURL
	}
	args := []string{
		"run", "update",
		"--executor-url", execURL,
		"--percent", fmt.Sprintf("%d", body.Percent),
		"--base-tps", fmt.Sprintf("%v", body.BaseTPS),
		"--ramp-up-seconds", fmt.Sprintf("%d", body.RampUpSeconds),
	}
	h.execCLIAndWrite(w, reqID, args...)
}

func (h *handler) runStatus(w http.ResponseWriter, r *http.Request) {
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)
	execURL := h.extractExecutorURL(r)
	h.execCLIAndWrite(w, reqID, "run", "status", "--executor-url", execURL)
}

func (h *handler) runReload(w http.ResponseWriter, r *http.Request) {
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)
	execURL := h.extractExecutorURL(r)
	h.execCLIAndWrite(w, reqID, "run", "reload", "--executor-url", execURL)
}

func (h *handler) runResetMetrics(w http.ResponseWriter, r *http.Request) {
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)
	execURL := h.extractExecutorURL(r)
	h.execCLIAndWrite(w, reqID, "run", "reset-metrics", "--executor-url", execURL)
}

func (h *handler) runStop(w http.ResponseWriter, r *http.Request) {
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)
	execURL := h.extractExecutorURL(r)
	h.execCLIAndWrite(w, reqID, "run", "stop", "--executor-url", execURL)
}

func (h *handler) executorsStart(w http.ResponseWriter, r *http.Request) {
	var body executorsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON body")
		return
	}
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)

	scenario := strings.TrimSpace(body.Scenario)
	if scenario == "" {
		writeError(w, "scenario is required")
		return
	}

	args := []string{"executors", "start", "--scenario", scenario}
	if strings.TrimSpace(body.Addr) != "" {
		args = append(args, "--addr", strings.TrimSpace(body.Addr))
	}
	if strings.TrimSpace(body.Bin) != "" {
		args = append(args, "--bin", strings.TrimSpace(body.Bin))
	}
	h.execCLIAndWrite(w, reqID, args...)
}

func (h *handler) executorsList(w http.ResponseWriter, r *http.Request) {
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)
	h.execCLIAndWrite(w, reqID, "executors", "list")
}

func (h *handler) executorsStop(w http.ResponseWriter, r *http.Request) {
	var body executorsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON body")
		return
	}
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)

	args := []string{"executors", "stop"}
	if body.PID > 0 {
		args = append(args, "--pid", fmt.Sprintf("%d", body.PID))
	} else if strings.TrimSpace(body.Addr) != "" {
		args = append(args, "--addr", strings.TrimSpace(body.Addr))
	} else {
		writeError(w, "pid or addr is required")
		return
	}
	h.execCLIAndWrite(w, reqID, args...)
}

func (h *handler) executorsRestart(w http.ResponseWriter, r *http.Request) {
	var body executorsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON body")
		return
	}
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)

	scenario := strings.TrimSpace(body.Scenario)
	if scenario == "" {
		writeError(w, "scenario is required")
		return
	}

	args := []string{"executors", "restart", "--scenario", scenario}
	if strings.TrimSpace(body.Addr) != "" {
		args = append(args, "--addr", strings.TrimSpace(body.Addr))
	}
	if strings.TrimSpace(body.Bin) != "" {
		args = append(args, "--bin", strings.TrimSpace(body.Bin))
	}
	if strings.TrimSpace(body.ExecutorURL) != "" {
		args = append(args, "--executor-url", strings.TrimSpace(body.ExecutorURL))
	}
	h.execCLIAndWrite(w, reqID, args...)
}

func (h *handler) extractExecutorURL(r *http.Request) string {
	var body runBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.ExecutorURL) != "" {
		return body.ExecutorURL
	}
	return h.cfg.DefaultExecutorURL
}

func (h *handler) scenariosList(w http.ResponseWriter, r *http.Request) {
	var body crudBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)
	args := []string{"scenarios", "list"}
	if strings.TrimSpace(body.Dir) != "" {
		args = append(args, "--dir", body.Dir)
	}
	h.execCLIAndWrite(w, reqID, args...)
}

func (h *handler) scenariosRead(w http.ResponseWriter, r *http.Request) {
	h.scenariosCRUD(w, r, "read")
}
func (h *handler) scenariosCreate(w http.ResponseWriter, r *http.Request) {
	h.scenariosCRUD(w, r, "create")
}
func (h *handler) scenariosUpdate(w http.ResponseWriter, r *http.Request) {
	h.scenariosCRUD(w, r, "update")
}
func (h *handler) scenariosDelete(w http.ResponseWriter, r *http.Request) {
	h.scenariosCRUD(w, r, "delete")
}

func (h *handler) scenariosCRUD(w http.ResponseWriter, r *http.Request, action string) {
	var body crudBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON body")
		return
	}
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)

	args := []string{"scenarios", action}
	if strings.TrimSpace(body.Path) != "" {
		args = append(args, "--path", normalizePathForDir(body.Dir, body.Path))
	}
	if strings.TrimSpace(body.Dir) != "" {
		args = append(args, "--dir", body.Dir)
	}
	if strings.TrimSpace(body.Content) != "" {
		args = append(args, "--content", body.Content)
	}
	if strings.TrimSpace(body.FromFile) != "" {
		args = append(args, "--from-file", body.FromFile)
	}
	if body.Force {
		args = append(args, "--force")
	}
	if body.Yes {
		args = append(args, "--yes")
	}
	h.execCLIAndWrite(w, reqID, args...)
}

func (h *handler) templatesList(w http.ResponseWriter, r *http.Request) {
	var body crudBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)
	args := []string{"templates", "list"}
	if strings.TrimSpace(body.Dir) != "" {
		args = append(args, "--dir", body.Dir)
	}
	h.execCLIAndWrite(w, reqID, args...)
}

func (h *handler) templatesRead(w http.ResponseWriter, r *http.Request) {
	h.templatesCRUD(w, r, "read")
}
func (h *handler) templatesCreate(w http.ResponseWriter, r *http.Request) {
	h.templatesCRUD(w, r, "create")
}
func (h *handler) templatesUpdate(w http.ResponseWriter, r *http.Request) {
	h.templatesCRUD(w, r, "update")
}
func (h *handler) templatesDelete(w http.ResponseWriter, r *http.Request) {
	h.templatesCRUD(w, r, "delete")
}

func (h *handler) templatesCRUD(w http.ResponseWriter, r *http.Request, action string) {
	var body crudBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON body")
		return
	}
	reqID := requestIDFromHeader(r)
	w.Header().Set("X-Request-Id", reqID)

	args := []string{"templates", action}
	if strings.TrimSpace(body.Path) != "" {
		args = append(args, "--path", normalizePathForDir(body.Dir, body.Path))
	}
	if strings.TrimSpace(body.Dir) != "" {
		args = append(args, "--dir", body.Dir)
	}
	if strings.TrimSpace(body.Content) != "" {
		args = append(args, "--content", body.Content)
	}
	if strings.TrimSpace(body.FromFile) != "" {
		args = append(args, "--from-file", body.FromFile)
	}
	if body.Force {
		args = append(args, "--force")
	}
	if body.Yes {
		args = append(args, "--yes")
	}
	h.execCLIAndWrite(w, reqID, args...)
}

func (h *handler) execCLIAndWrite(w http.ResponseWriter, requestID string, commandArgs ...string) {
	args := make([]string, 0, len(h.cfg.CLIArgs)+len(commandArgs)+4)
	args = append(args, h.cfg.CLIArgs...)
	args = append(args, "--output", "json", "--request-id", requestID)
	args = append(args, commandArgs...)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(h.cfg.CLITimeoutSeconds)*time.Second)
	defer cancel()

	startedAt := time.Now()
	log.Printf("[backend][request_id=%s] exec cli start: cmd=%s args=%q workdir=%s timeout=%ds",
		requestID, h.cfg.CLICommand, args, h.cfg.CLIWorkDir, h.cfg.CLITimeoutSeconds)

	cmd := exec.CommandContext(ctx, h.cfg.CLICommand, args...)
	cmd.Dir = h.cfg.CLIWorkDir
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(startedAt)
	trimmedOut := strings.TrimSpace(string(out))
	if err != nil {
		log.Printf("[backend][request_id=%s] exec cli failed: elapsed=%s err=%v output=%q",
			requestID, elapsed, err, trimForLog(trimmedOut, maxLoggedOutput))
		writeError(w, fmt.Sprintf("cli failed: %v; output: %s", err, strings.TrimSpace(string(out))))
		return
	}
	log.Printf("[backend][request_id=%s] exec cli done: elapsed=%s output=%q",
		requestID, elapsed, trimForLog(trimmedOut, maxLoggedOutput))

	var cliResp any
	if err := json.Unmarshal(out, &cliResp); err != nil {
		// Some CLI commands print plain text (e.g. list/read CRUD output).
		trimmed := strings.TrimSpace(string(out))
		lines := make([]string, 0)
		if trimmed != "" {
			for _, line := range strings.Split(trimmed, "\n") {
				v := strings.TrimSpace(line)
				if v != "" {
					lines = append(lines, v)
				}
			}
		}
		writeSuccess(w, map[string]any{
			"status": "success",
			"data": map[string]any{
				"stdout": trimmed,
				"lines":  lines,
			},
		})
		return
	}
	writeSuccess(w, cliResp)
}

func writeSuccess(w http.ResponseWriter, data any) {
	writeJSON(w, apiResponse{
		Status: "success",
		Data:   data,
	})
}

func writeError(w http.ResponseWriter, err string) {
	writeJSON(w, apiResponse{
		Status: "error",
		Error:  err,
	})
}

func writeJSON(w http.ResponseWriter, payload apiResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

func trimForLog(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max < 32 {
		return s[:max]
	}
	head := max - 24
	return s[:head] + "...(truncated)"
}

// normalizePathForDir removes duplicated directory prefix in path when
// both `dir` and `path` are provided (e.g. dir=scenarios, path=scenarios\file.yml).
func normalizePathForDir(dir, path string) string {
	d := strings.TrimSpace(dir)
	p := strings.TrimSpace(path)
	if d == "" || p == "" || filepath.IsAbs(p) {
		return p
	}

	cleanDir := filepath.Clean(d)
	cleanPath := filepath.Clean(p)
	prefix := cleanDir + string(os.PathSeparator)
	if strings.HasPrefix(cleanPath, prefix) {
		return strings.TrimPrefix(cleanPath, prefix)
	}

	// Also handle mixed slash styles from browser/backend.
	slashDir := filepath.ToSlash(cleanDir)
	slashPath := filepath.ToSlash(cleanPath)
	if strings.HasPrefix(slashPath, slashDir+"/") {
		return strings.TrimPrefix(slashPath, slashDir+"/")
	}
	return p
}
