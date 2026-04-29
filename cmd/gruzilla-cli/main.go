package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"gruzilla/internal/executor"
	"gruzilla/internal/scenario"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const defaultExecutorURL = "http://localhost:8081"

func defaultExecutorBin() string {
	for _, candidate := range executorExecutableCandidates() {
		if pathExists(candidate) {
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs
			}
			return candidate
		}
	}
	return "go"
}

func executorExecutableCandidates() []string {
	name := "gruzilla-executor"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := []string{name, filepath.Join("bin", name)}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		out = append(out, filepath.Join(exeDir, name))
	}
	return uniqueNonEmptyPaths(out)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func uniqueNonEmptyPaths(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		p := strings.TrimSpace(it)
		if p == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(p))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
}

// varsFlag реализует кастомный парсер repeatable флага --var key=value.
type varsFlag map[string]string

func (v *varsFlag) String() string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", map[string]string(*v))
}

func (v *varsFlag) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid --var format, expected key=value")
	}
	key := strings.TrimSpace(parts[0])
	if key == "" {
		return fmt.Errorf("var key cannot be empty")
	}
	if *v == nil {
		*v = make(map[string]string)
	}
	(*v)[key] = strings.TrimSpace(parts[1])
	return nil
}

func (v *varsFlag) Type() string {
	return "vars"
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// newRootCmd собирает корневую команду CLI и глобальные флаги.
func newRootCmd() *cobra.Command {
	var output string
	var executorURL string
	var requestID string
	var verbose bool

	root := &cobra.Command{
		Use:   "gruzilla-cli",
		Short: "Gruzilla CLI",
	}

	root.PersistentFlags().StringVar(&output, "output", "text", "output format: text|json")
	root.PersistentFlags().StringVar(&executorURL, "executor-url", defaultExecutorURL, "executor base URL")
	root.PersistentFlags().StringVar(&requestID, "request-id", "", "request id (uuid)")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose logging")

	root.AddCommand(newRunCmd(&executorURL, &output))
	root.AddCommand(newExecutorsCmd(&output))
	root.AddCommand(newScenariosCmd())
	root.AddCommand(newTemplatesCmd())

	return root
}

// newRunCmd объединяет runtime-операции над уже запущенным executor.
func newRunCmd(executorURL *string, output *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Manage load on executor",
	}
	cmd.AddCommand(newRunStartCmd(executorURL, output))
	cmd.AddCommand(newRunStopCmd(executorURL, output))
	cmd.AddCommand(newRunStatusCmd(executorURL, output))
	cmd.AddCommand(newRunUpdateCmd(executorURL, output))
	cmd.AddCommand(newRunReloadCmd(executorURL, output))
	cmd.AddCommand(newRunResetMetricsCmd(executorURL, output))
	return cmd
}

// newExecutorsCmd объединяет команды lifecycle самого процесса executor.
func newExecutorsCmd(output *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "executors",
		Short: "Manage executor processes",
	}
	cmd.AddCommand(newExecutorsListCmd(output))
	cmd.AddCommand(newExecutorsStartCmd(output))
	cmd.AddCommand(newExecutorsStopCmd(output))
	cmd.AddCommand(newExecutorsRestartCmd(output))
	return cmd
}

func newExecutorsListCmd(output *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List running gruzilla-executor processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := listExecutors()
			if err != nil {
				return err
			}
			if strings.EqualFold(strings.TrimSpace(*output), "json") {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(items)
			}
			for _, it := range items {
				fmt.Printf("pid=%d addr=%s scenario=%s cmd=%s\n", it.PID, it.Addr, it.Scenario, it.CommandLine)
			}
			return nil
		},
	}
}

type executorProc struct {
	PID         int
	Addr        string
	Scenario    string
	CommandLine string
}

func newExecutorsStopCmd(output *string) *cobra.Command {
	var addr string
	var pid int
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop running executor by addr or pid",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(addr) == "" && pid <= 0 {
				return fmt.Errorf("provide --addr or --pid")
			}
			executors, err := listExecutors()
			if err != nil {
				return err
			}

			targets := make([]executorProc, 0)
			if pid > 0 {
				for _, ex := range executors {
					if ex.PID == pid {
						targets = append(targets, ex)
					}
				}
			} else {
				want := normalizeAddr(addr)
				for _, ex := range executors {
					if normalizeAddr(ex.Addr) == want {
						targets = append(targets, ex)
					}
				}
			}

			if len(targets) == 0 {
				return fmt.Errorf("executor not found (addr=%s pid=%d)", addr, pid)
			}

			stopped := make([]executorProc, 0, len(targets))
			for _, ex := range targets {
				if err := killProcess(ex.PID); err != nil {
					return fmt.Errorf("stop executor pid=%d: %w", ex.PID, err)
				}
				stopped = append(stopped, ex)
			}

			if strings.EqualFold(strings.TrimSpace(*output), "json") {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"stopped": stopped,
				})
			}
			for _, ex := range stopped {
				fmt.Printf("stopped pid=%d addr=%s scenario=%s\n", ex.PID, ex.Addr, ex.Scenario)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "executor addr, e.g. :8081")
	cmd.Flags().IntVar(&pid, "pid", 0, "executor process id")
	return cmd
}

func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func listExecutors() ([]executorProc, error) {
	switch runtime.GOOS {
	case "windows":
		return listExecutorsWindows()
	default:
		return listExecutorsUnix()
	}
}

func listExecutorsWindows() ([]executorProc, error) {
	psCmd := `Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -match 'gruzilla-executor' } | Select-Object ProcessId,CommandLine | ConvertTo-Json -Compress`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", psCmd).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list executors (windows): %w; output: %s", err, strings.TrimSpace(string(out)))
	}
	payload := strings.TrimSpace(string(out))
	if payload == "" || payload == "null" {
		return nil, nil
	}

	var list []struct {
		ProcessID   int    `json:"ProcessId"`
		CommandLine string `json:"CommandLine"`
	}
	if strings.HasPrefix(payload, "[") {
		if err := json.Unmarshal([]byte(payload), &list); err != nil {
			return nil, fmt.Errorf("parse process list: %w", err)
		}
	} else {
		var one struct {
			ProcessID   int    `json:"ProcessId"`
			CommandLine string `json:"CommandLine"`
		}
		if err := json.Unmarshal([]byte(payload), &one); err != nil {
			return nil, fmt.Errorf("parse process item: %w", err)
		}
		list = append(list, one)
	}

	outList := make([]executorProc, 0, len(list))
	for _, p := range list {
		if shouldSkipExecutorCmd(p.CommandLine) {
			continue
		}
		addr := extractFlagValue(p.CommandLine, "--addr")
		scenario := extractFlagValue(p.CommandLine, "--scenario")
		outList = append(outList, executorProc{
			PID:         p.ProcessID,
			Addr:        addr,
			Scenario:    scenario,
			CommandLine: strings.TrimSpace(p.CommandLine),
		})
	}
	return dedupeExecutors(outList), nil
}

func listExecutorsUnix() ([]executorProc, error) {
	out, err := exec.Command("ps", "-eo", "pid,args").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list executors (unix): %w; output: %s", err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	outList := make([]executorProc, 0)
	for _, line := range lines {
		if !strings.Contains(line, "gruzilla-executor") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid := 0
		_, _ = fmt.Sscanf(fields[0], "%d", &pid)
		cmdLine := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if shouldSkipExecutorCmd(cmdLine) {
			continue
		}
		outList = append(outList, executorProc{
			PID:         pid,
			Addr:        extractFlagValue(cmdLine, "--addr"),
			Scenario:    extractFlagValue(cmdLine, "--scenario"),
			CommandLine: cmdLine,
		})
	}
	return dedupeExecutors(outList), nil
}

func extractFlagValue(commandLine, flag string) string {
	// Supports both:
	//   --flag value
	//   --flag=value
	// and quoted values with spaces.
	re := regexp.MustCompile(regexp.QuoteMeta(flag) + `(?:\s+|=)("([^"]+)"|(\S+))`)
	m := re.FindStringSubmatch(commandLine)
	if len(m) == 0 {
		return ""
	}
	if len(m) > 2 && m[2] != "" {
		return m[2]
	}
	if len(m) > 3 {
		return m[3]
	}
	return ""
}

func shouldSkipExecutorCmd(cmdLine string) bool {
	c := strings.ToLower(strings.TrimSpace(cmdLine))
	if c == "" {
		return true
	}
	// Exclude the PowerShell command used to inspect processes.
	if strings.Contains(c, "get-ciminstance win32_process") && strings.Contains(c, "convertto-json") {
		return true
	}
	return false
}

func dedupeExecutors(items []executorProc) []executorProc {
	if len(items) <= 1 {
		return items
	}

	bestByKey := make(map[string]executorProc, len(items))
	for _, it := range items {
		key := strings.TrimSpace(it.Addr) + "|" + strings.TrimSpace(it.Scenario)
		if key == "|" {
			key = fmt.Sprintf("pid:%d", it.PID)
		}
		if cur, ok := bestByKey[key]; !ok || executorScore(it) > executorScore(cur) {
			bestByKey[key] = it
		}
	}

	out := make([]executorProc, 0, len(bestByKey))
	for _, v := range bestByKey {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

func executorScore(it executorProc) int {
	c := strings.ToLower(it.CommandLine)
	// Prefer real executor process over "go run ...".
	if strings.Contains(c, "go run") && strings.Contains(c, "gruzilla-executor") {
		return 1
	}
	if strings.Contains(c, "gruzilla-executor.exe") || strings.Contains(c, "/gruzilla-executor") || strings.Contains(c, "\\gruzilla-executor") {
		return 3
	}
	return 2
}

func normalizeAddr(addr string) string {
	a := strings.TrimSpace(addr)
	if a == "" {
		return ""
	}
	if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
		return a
	}
	if strings.HasPrefix(a, ":") {
		return "localhost" + a
	}
	return a
}

// newScenariosCmd объединяет CRUD-операции над YAML-сценариями на диске.
func newScenariosCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scenarios",
		Short: "Manage scenario YAML files (CRUD)",
	}
	cmd.AddCommand(newScenariosListCmd())
	cmd.AddCommand(newScenariosReadCmd())
	cmd.AddCommand(newScenariosCreateCmd())
	cmd.AddCommand(newScenariosUpdateCmd())
	cmd.AddCommand(newScenariosDeleteCmd())
	return cmd
}

func newScenariosListCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List scenario YAML files",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(dir) == "" {
				dir = "scenarios"
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return fmt.Errorf("read dir %q: %w", dir, err)
			}
			files := make([]string, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				lower := strings.ToLower(name)
				if strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml") {
					files = append(files, filepath.Join(dir, name))
				}
			}
			sort.Strings(files)
			for _, f := range files {
				fmt.Println(f)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "scenarios", "scenarios directory")
	return cmd
}

func newScenariosReadCmd() *cobra.Command {
	var path string
	var dir string
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read scenario YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveScenarioPath(path, dir)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Errorf("read scenario %q: %w", resolved, err)
			}
			fmt.Print(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "scenario path (relative to --dir or absolute)")
	cmd.Flags().StringVar(&dir, "dir", "scenarios", "base directory for relative paths")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func newScenariosCreateCmd() *cobra.Command {
	var path string
	var dir string
	var name string
	var description string
	var content string
	var fromFile string
	var force bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create scenario YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveScenarioPath(path, dir)
			if err != nil {
				return err
			}
			if filepath.Ext(resolved) == "" {
				resolved += ".yml"
			}
			if !force {
				if _, err := os.Stat(resolved); err == nil {
					return fmt.Errorf("file already exists: %s (use --force to overwrite)", resolved)
				}
			}

			data, err := buildScenarioContent(resolved, name, description, content, fromFile)
			if err != nil {
				return err
			}
			if err := validateScenarioYAML(data); err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
				return fmt.Errorf("create parent dir: %w", err)
			}
			if err := os.WriteFile(resolved, data, 0o644); err != nil {
				return fmt.Errorf("write scenario %q: %w", resolved, err)
			}
			fmt.Printf("created: %s\n", resolved)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "scenario path (relative to --dir or absolute)")
	cmd.Flags().StringVar(&dir, "dir", "scenarios", "base directory for relative paths")
	cmd.Flags().StringVar(&name, "name", "", "scenario name for auto-generated template")
	cmd.Flags().StringVar(&description, "description", "", "scenario description for auto-generated template")
	cmd.Flags().StringVar(&content, "content", "", "raw YAML content to write")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "read YAML content from file")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite target file if it already exists")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func newScenariosUpdateCmd() *cobra.Command {
	var path string
	var dir string
	var content string
	var fromFile string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update existing scenario YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveScenarioPath(path, dir)
			if err != nil {
				return err
			}
			if _, err := os.Stat(resolved); err != nil {
				return fmt.Errorf("scenario file does not exist: %s", resolved)
			}
			if strings.TrimSpace(content) == "" && strings.TrimSpace(fromFile) == "" {
				return fmt.Errorf("provide one of --content or --from-file")
			}
			if strings.TrimSpace(content) != "" && strings.TrimSpace(fromFile) != "" {
				return fmt.Errorf("use only one source: --content or --from-file")
			}

			var data []byte
			if strings.TrimSpace(fromFile) != "" {
				data, err = os.ReadFile(fromFile)
				if err != nil {
					return fmt.Errorf("read --from-file %q: %w", fromFile, err)
				}
			} else {
				data = []byte(content)
			}
			if err := validateScenarioYAML(data); err != nil {
				return err
			}
			if err := os.WriteFile(resolved, data, 0o644); err != nil {
				return fmt.Errorf("write scenario %q: %w", resolved, err)
			}
			fmt.Printf("updated: %s\n", resolved)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "scenario path (relative to --dir or absolute)")
	cmd.Flags().StringVar(&dir, "dir", "scenarios", "base directory for relative paths")
	cmd.Flags().StringVar(&content, "content", "", "raw YAML content to write")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "read YAML content from file")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func newScenariosDeleteCmd() *cobra.Command {
	var path string
	var dir string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete scenario YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveScenarioPath(path, dir)
			if err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("deletion requires --yes flag")
			}
			if err := os.Remove(resolved); err != nil {
				return fmt.Errorf("delete scenario %q: %w", resolved, err)
			}
			fmt.Printf("deleted: %s\n", resolved)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "scenario path (relative to --dir or absolute)")
	cmd.Flags().StringVar(&dir, "dir", "scenarios", "base directory for relative paths")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func resolveScenarioPath(path string, dir string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", fmt.Errorf("--path is required")
	}
	if filepath.IsAbs(p) {
		return p, nil
	}
	d := strings.TrimSpace(dir)
	if d == "" {
		d = "scenarios"
	}
	return filepath.Join(d, p), nil
}

func buildScenarioContent(targetPath, name, description, content, fromFile string) ([]byte, error) {
	if strings.TrimSpace(content) != "" && strings.TrimSpace(fromFile) != "" {
		return nil, fmt.Errorf("use only one source: --content or --from-file")
	}
	if strings.TrimSpace(fromFile) != "" {
		data, err := os.ReadFile(fromFile)
		if err != nil {
			return nil, fmt.Errorf("read --from-file %q: %w", fromFile, err)
		}
		return data, nil
	}
	if strings.TrimSpace(content) != "" {
		return []byte(content), nil
	}

	baseName := strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))
	if strings.TrimSpace(name) == "" {
		name = baseName
	}
	sc := scenario.Scenario{
		Name:        name,
		Description: description,
		Steps: []scenario.Step{
			{
				Type:   "rest",
				Name:   "example-rest-step",
				Method: "POST",
				URL:    "http://localhost:8080/health",
				Body:   "{}",
			},
		},
	}
	data, err := yaml.Marshal(&sc)
	if err != nil {
		return nil, fmt.Errorf("marshal scenario template: %w", err)
	}
	return data, nil
}

func validateScenarioYAML(data []byte) error {
	var sc scenario.Scenario
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	if err := scenario.Validate(sc); err != nil {
		return fmt.Errorf("invalid scenario content: %w", err)
	}
	return nil
}

// newTemplatesCmd объединяет CRUD-операции над файлами шаблонов.
func newTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "Manage template files (CRUD)",
	}
	cmd.AddCommand(newTemplatesListCmd())
	cmd.AddCommand(newTemplatesReadCmd())
	cmd.AddCommand(newTemplatesCreateCmd())
	cmd.AddCommand(newTemplatesUpdateCmd())
	cmd.AddCommand(newTemplatesDeleteCmd())
	return cmd
}

func newTemplatesListCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List template files",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(dir) == "" {
				dir = "templates"
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return fmt.Errorf("read dir %q: %w", dir, err)
			}
			files := make([]string, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := strings.ToLower(e.Name())
				if strings.HasSuffix(name, ".tmpl") || strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".txt") {
					files = append(files, filepath.Join(dir, e.Name()))
				}
			}
			sort.Strings(files)
			for _, f := range files {
				fmt.Println(f)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "templates", "templates directory")
	return cmd
}

func newTemplatesReadCmd() *cobra.Command {
	var path string
	var dir string
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read template file",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveTemplatePath(path, dir)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Errorf("read template %q: %w", resolved, err)
			}
			fmt.Print(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "template path (relative to --dir or absolute)")
	cmd.Flags().StringVar(&dir, "dir", "templates", "base directory for relative paths")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func newTemplatesCreateCmd() *cobra.Command {
	var path string
	var dir string
	var content string
	var fromFile string
	var force bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create template file",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveTemplatePath(path, dir)
			if err != nil {
				return err
			}
			if filepath.Ext(resolved) == "" {
				resolved += ".tmpl"
			}
			if !force {
				if _, err := os.Stat(resolved); err == nil {
					return fmt.Errorf("file already exists: %s (use --force to overwrite)", resolved)
				}
			}

			data, err := buildTemplateContent(content, fromFile)
			if err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
				return fmt.Errorf("create parent dir: %w", err)
			}
			if err := os.WriteFile(resolved, data, 0o644); err != nil {
				return fmt.Errorf("write template %q: %w", resolved, err)
			}
			fmt.Printf("created: %s\n", resolved)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "template path (relative to --dir or absolute)")
	cmd.Flags().StringVar(&dir, "dir", "templates", "base directory for relative paths")
	cmd.Flags().StringVar(&content, "content", "", "raw template content to write")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "read template content from file")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite target file if it already exists")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func newTemplatesUpdateCmd() *cobra.Command {
	var path string
	var dir string
	var content string
	var fromFile string

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update existing template file",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveTemplatePath(path, dir)
			if err != nil {
				return err
			}
			if _, err := os.Stat(resolved); err != nil {
				return fmt.Errorf("template file does not exist: %s", resolved)
			}
			if strings.TrimSpace(content) == "" && strings.TrimSpace(fromFile) == "" {
				return fmt.Errorf("provide one of --content or --from-file")
			}
			if strings.TrimSpace(content) != "" && strings.TrimSpace(fromFile) != "" {
				return fmt.Errorf("use only one source: --content or --from-file")
			}

			var data []byte
			if strings.TrimSpace(fromFile) != "" {
				data, err = os.ReadFile(fromFile)
				if err != nil {
					return fmt.Errorf("read --from-file %q: %w", fromFile, err)
				}
			} else {
				data = []byte(content)
			}
			if len(data) == 0 {
				return fmt.Errorf("template content cannot be empty")
			}
			if err := os.WriteFile(resolved, data, 0o644); err != nil {
				return fmt.Errorf("write template %q: %w", resolved, err)
			}
			fmt.Printf("updated: %s\n", resolved)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "template path (relative to --dir or absolute)")
	cmd.Flags().StringVar(&dir, "dir", "templates", "base directory for relative paths")
	cmd.Flags().StringVar(&content, "content", "", "raw template content to write")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "read template content from file")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func newTemplatesDeleteCmd() *cobra.Command {
	var path string
	var dir string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete template file",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveTemplatePath(path, dir)
			if err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("deletion requires --yes flag")
			}
			if err := os.Remove(resolved); err != nil {
				return fmt.Errorf("delete template %q: %w", resolved, err)
			}
			fmt.Printf("deleted: %s\n", resolved)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "template path (relative to --dir or absolute)")
	cmd.Flags().StringVar(&dir, "dir", "templates", "base directory for relative paths")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func resolveTemplatePath(path string, dir string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", fmt.Errorf("--path is required")
	}
	if filepath.IsAbs(p) {
		return p, nil
	}
	d := strings.TrimSpace(dir)
	if d == "" {
		d = "templates"
	}
	return filepath.Join(d, p), nil
}

func buildTemplateContent(content, fromFile string) ([]byte, error) {
	if strings.TrimSpace(content) != "" && strings.TrimSpace(fromFile) != "" {
		return nil, fmt.Errorf("use only one source: --content or --from-file")
	}
	if strings.TrimSpace(fromFile) != "" {
		data, err := os.ReadFile(fromFile)
		if err != nil {
			return nil, fmt.Errorf("read --from-file %q: %w", fromFile, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("template content cannot be empty")
		}
		return data, nil
	}
	if strings.TrimSpace(content) != "" {
		return []byte(content), nil
	}
	return []byte("{\"requestId\":\"{{requestId}}\"}\n"), nil
}

// configureExecutorProcessIO направляет вывод дочернего executor:
// - text mode: в консоль текущего CLI;
// - json mode: в os.DevNull, чтобы process не зависел от жизненного цикла CLI.
// Важно для Windows: io.Discard использует pipe+goroutine и после выхода CLI
// запись в stdout/stderr может завершить дочерний процесс.
func configureExecutorProcessIO(proc *exec.Cmd, output string) error {
	if strings.EqualFold(strings.TrimSpace(output), "text") {
		proc.Stdout = os.Stdout
		proc.Stderr = os.Stderr
		return nil
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	proc.Stdout = devNull
	proc.Stderr = devNull
	return nil
}

// newExecutorsStartCmd поднимает новый процесс gruzilla-executor
// (через `go run` либо указанный бинарник).
func newExecutorsStartCmd(output *string) *cobra.Command {
	var scenarioPath string
	var addr string
	var bin string
	var logFile string
	var logMaxSizeMB int
	var logMaxBackups int
	var logMaxAgeDays int

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start gruzilla-executor process for a scenario",
		RunE: func(cmd *cobra.Command, args []string) error {
			if scenarioPath == "" {
				return fmt.Errorf("--scenario is required")
			}
			if addr == "" {
				addr = ":8081"
			}
			normAddr := normalizeAddr(addr)

			if list, err := listExecutors(); err == nil {
				for _, ex := range list {
					if normalizeAddr(ex.Addr) == normAddr {
						return fmt.Errorf("executor already running on addr=%s (pid=%d, scenario=%s); use another --addr or executors restart", addr, ex.PID, ex.Scenario)
					}
				}
			}

			var execCmd string
			var execArgs []string

			if bin == "" || bin == "go" {
				execCmd = "go"
				execArgs = []string{"run", "./cmd/gruzilla-executor", "--scenario", scenarioPath, "--addr", addr}
			} else {
				execCmd = bin
				execArgs = []string{"--scenario", scenarioPath, "--addr", addr}
			}
			if strings.TrimSpace(logFile) != "" {
				execArgs = append(execArgs, "--log-file", strings.TrimSpace(logFile))
			}
			if logMaxSizeMB > 0 {
				execArgs = append(execArgs, "--log-max-size-mb", fmt.Sprintf("%d", logMaxSizeMB))
			}
			if logMaxBackups >= 0 {
				execArgs = append(execArgs, "--log-max-backups", fmt.Sprintf("%d", logMaxBackups))
			}
			if logMaxAgeDays >= 0 {
				execArgs = append(execArgs, "--log-max-age-days", fmt.Sprintf("%d", logMaxAgeDays))
			}

			proc := exec.Command(execCmd, execArgs...)
			// Для backend (--output json) отделяем I/O executor от stdout/stderr CLI.
			if err := configureExecutorProcessIO(proc, *output); err != nil {
				return err
			}

			if err := proc.Start(); err != nil {
				return fmt.Errorf("start executor process: %w", err)
			}

			fmt.Printf("executor started: pid=%d addr=%s scenario=%s\n", proc.Process.Pid, addr, scenarioPath)
			fmt.Printf("use --executor-url http://localhost%s with run commands\n", addr)
			return nil
		},
	}

	cmd.Flags().StringVar(&scenarioPath, "scenario", "", "path to .yml scenario file")
	cmd.Flags().StringVar(&addr, "addr", ":8081", "listen address for executor (e.g. :8081)")
	cmd.Flags().StringVar(&bin, "bin", defaultExecutorBin(), "executor binary path (default: local gruzilla-executor[.exe], fallback: 'go')")
	cmd.Flags().StringVar(&logFile, "log-file", "", "optional path to executor log file")
	cmd.Flags().IntVar(&logMaxSizeMB, "log-max-size-mb", 0, "max log file size in MB before rotate (0 = executor default)")
	cmd.Flags().IntVar(&logMaxBackups, "log-max-backups", -1, "max rotated log files to keep (-1 = executor default)")
	cmd.Flags().IntVar(&logMaxAgeDays, "log-max-age-days", -1, "max age of rotated logs in days (-1 = executor default)")

	return cmd
}

// newExecutorsRestartCmd делает мягкий перезапуск: сначала shutdown через API,
// затем стартует новый процесс executor с тем же сценарием/адресом.
func newExecutorsRestartCmd(output *string) *cobra.Command {
	var scenarioPath string
	var addr string
	var bin string
	var executorURL string
	var logFile string
	var logMaxSizeMB int
	var logMaxBackups int
	var logMaxAgeDays int

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Shutdown current executor and start a new one (same scenario and addr)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if scenarioPath == "" {
				return fmt.Errorf("--scenario is required")
			}
			if addr == "" {
				addr = ":8081"
			}
			url := executorURL
			if url == "" {
				url = "http://localhost" + addr
			}

			client := executor.NewClient(url, 5*time.Second)
			_, _ = client.Call("/api/v1/shutdown", map[string]any{})
			time.Sleep(2 * time.Second)

			var execCmd string
			var execArgs []string
			if bin == "" || bin == "go" {
				execCmd = "go"
				execArgs = []string{"run", "./cmd/gruzilla-executor", "--scenario", scenarioPath, "--addr", addr}
			} else {
				execCmd = bin
				execArgs = []string{"--scenario", scenarioPath, "--addr", addr}
			}
			if strings.TrimSpace(logFile) != "" {
				execArgs = append(execArgs, "--log-file", strings.TrimSpace(logFile))
			}
			if logMaxSizeMB > 0 {
				execArgs = append(execArgs, "--log-max-size-mb", fmt.Sprintf("%d", logMaxSizeMB))
			}
			if logMaxBackups >= 0 {
				execArgs = append(execArgs, "--log-max-backups", fmt.Sprintf("%d", logMaxBackups))
			}
			if logMaxAgeDays >= 0 {
				execArgs = append(execArgs, "--log-max-age-days", fmt.Sprintf("%d", logMaxAgeDays))
			}
			proc := exec.Command(execCmd, execArgs...)
			if err := configureExecutorProcessIO(proc, *output); err != nil {
				return err
			}
			if err := proc.Start(); err != nil {
				return fmt.Errorf("start executor: %w", err)
			}
			fmt.Printf("executor restarted: pid=%d addr=%s scenario=%s\n", proc.Process.Pid, addr, scenarioPath)
			fmt.Printf("use --executor-url %s with run commands\n", url)
			return nil
		},
	}
	cmd.Flags().StringVar(&scenarioPath, "scenario", "", "path to .yml scenario file")
	cmd.Flags().StringVar(&addr, "addr", ":8081", "listen address for executor")
	cmd.Flags().StringVar(&bin, "bin", defaultExecutorBin(), "executor binary path (default: local gruzilla-executor[.exe], fallback: 'go')")
	cmd.Flags().StringVar(&executorURL, "executor-url", "", "URL of running executor to shutdown (default http://localhost<addr>)")
	cmd.Flags().StringVar(&logFile, "log-file", "", "optional path to executor log file")
	cmd.Flags().IntVar(&logMaxSizeMB, "log-max-size-mb", 0, "max log file size in MB before rotate (0 = executor default)")
	cmd.Flags().IntVar(&logMaxBackups, "log-max-backups", -1, "max rotated log files to keep (-1 = executor default)")
	cmd.Flags().IntVar(&logMaxAgeDays, "log-max-age-days", -1, "max age of rotated logs in days (-1 = executor default)")
	return cmd
}

// newRunStartCmd запускает генерацию нагрузки на executor.
func newRunStartCmd(executorURL *string, output *string) *cobra.Command {
	var percent int
	var baseTPS float64
	var rampUp int
	var variables varsFlag
	var ignoreLoadSchedule bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start load generation",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := executor.NewClient(*executorURL, 10*time.Second)
			body := map[string]any{
				"percent":         percent,
				"base_tps":        baseTPS,
				"ramp_up_seconds": rampUp,
				"variables":       map[string]string(variables),
			}
			if ignoreLoadSchedule {
				body["ignore_load_schedule"] = true
			}
			resp, err := client.Call("/api/v1/start", body)
			if err != nil {
				return err
			}
			return printAPIResponse(resp, *output)
		},
	}

	cmd.Flags().IntVar(&percent, "percent", 100, "load coefficient in percent (1-500)")
	cmd.Flags().Float64Var(&baseTPS, "base-tps", 10, "base TPS of scenario")
	cmd.Flags().IntVar(&rampUp, "ramp-up-seconds", 0, "linear ramp-up duration in seconds (0 = no ramp)")
	cmd.Flags().BoolVar(&ignoreLoadSchedule, "ignore-load-schedule", false, "ignore scenario load_schedule; use base-tps × percent only")
	cmd.Flags().Var(&variables, "var", "scenario variable in key=value format (repeatable)")

	return cmd
}

// newRunStopCmd останавливает текущий прогон нагрузки.
func newRunStopCmd(executorURL *string, output *string) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop load generation",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := executor.NewClient(*executorURL, 10*time.Second)
			resp, err := client.Call("/api/v1/stop", map[string]any{})
			if err != nil {
				return err
			}
			return printAPIResponse(resp, *output)
		},
	}
}

// newRunStatusCmd запрашивает текущий статус и метрики executor.
func newRunStatusCmd(executorURL *string, output *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Get executor status",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := executor.NewClient(*executorURL, 10*time.Second)
			resp, err := client.Call("/api/v1/status", map[string]any{})
			if err != nil {
				return err
			}
			return printAPIResponse(resp, *output)
		},
	}
}

// newRunUpdateCmd меняет параметры нагрузки без остановки runLoop.
func newRunUpdateCmd(executorURL *string, output *string) *cobra.Command {
	var percent int
	var baseTPS float64
	var rampUp int
	var ignoreLoadSchedule bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update load configuration (percent/base TPS/ramp-up) without restart",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := executor.NewClient(*executorURL, 10*time.Second)
			body := map[string]any{
				"percent":         percent,
				"base_tps":        baseTPS,
				"ramp_up_seconds": rampUp,
			}
			if cmd.Flags().Changed("ignore-load-schedule") {
				body["ignore_load_schedule"] = ignoreLoadSchedule
			}
			resp, err := client.Call("/api/v1/update", body)
			if err != nil {
				return err
			}
			return printAPIResponse(resp, *output)
		},
	}

	cmd.Flags().IntVar(&percent, "percent", 0, "load coefficient in percent (1-500). 0 = no change")
	cmd.Flags().Float64Var(&baseTPS, "base-tps", 0, "base TPS of scenario. 0 = no change")
	cmd.Flags().IntVar(&rampUp, "ramp-up-seconds", 0, "linear ramp-up duration in seconds (0 = no change)")
	cmd.Flags().BoolVar(&ignoreLoadSchedule, "ignore-load-schedule", false, "set whether to ignore scenario load_schedule (use --ignore-load-schedule=false to follow schedule)")

	return cmd
}

// newRunResetMetricsCmd обнуляет counters и last_error
// (только когда нагрузка остановлена).
func newRunResetMetricsCmd(executorURL *string, output *string) *cobra.Command {
	return &cobra.Command{
		Use:   "reset-metrics",
		Short: "Reset counters and last_error (only when load is stopped)",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := executor.NewClient(*executorURL, 10*time.Second)
			resp, err := client.Call("/api/v1/reset_metrics", map[string]any{})
			if err != nil {
				return err
			}
			return printAPIResponse(resp, *output)
		},
	}
}

// newRunReloadCmd перечитывает YAML-сценарий на стороне executor.
func newRunReloadCmd(executorURL *string, output *string) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Reload scenario YAML on executor without restarting process",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := executor.NewClient(*executorURL, 10*time.Second)
			resp, err := client.Call("/api/v1/reload", map[string]any{})
			if err != nil {
				return err
			}
			return printAPIResponse(resp, *output)
		},
	}
}

// printAPIResponse печатает стандартизованный ответ executor API
// в текстовом или JSON-формате.
func printAPIResponse(resp executor.APIResponse, output string) error {
	if output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if resp.Status == "error" {
		fmt.Printf("status: error\nerror: %s\n", resp.Error)
		return nil
	}

	fmt.Println("status: success")
	if len(resp.Data) > 0 {
		fmt.Printf("data: %s\n", string(resp.Data))
	}
	return nil
}
