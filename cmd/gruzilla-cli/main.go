package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gruzilla/internal/executor"

	"github.com/spf13/cobra"
)

const defaultExecutorURL = "http://localhost:8081"

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

	return root
}

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

func newExecutorsCmd(output *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "executors",
		Short: "Manage executor processes",
	}
	cmd.AddCommand(newExecutorsStartCmd(output))
	cmd.AddCommand(newExecutorsRestartCmd(output))
	return cmd
}

func newExecutorsStartCmd(output *string) *cobra.Command {
	var scenarioPath string
	var addr string
	var bin string

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

			var execCmd string
			var execArgs []string

			if bin == "" || bin == "go" {
				execCmd = "go"
				execArgs = []string{"run", "./cmd/gruzilla-executor", "--scenario", scenarioPath, "--addr", addr}
			} else {
				execCmd = bin
				execArgs = []string{"--scenario", scenarioPath, "--addr", addr}
			}

			proc := exec.Command(execCmd, execArgs...)
			// Наследуем stdout/stderr, чтобы видеть логи executor'а в той же консоли
			proc.Stdout = os.Stdout
			proc.Stderr = os.Stderr

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
	cmd.Flags().StringVar(&bin, "bin", "go", "executor binary or 'go' to use 'go run ./cmd/gruzilla-executor'")

	return cmd
}

func newExecutorsRestartCmd(output *string) *cobra.Command {
	var scenarioPath string
	var addr string
	var bin string
	var executorURL string

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
			proc := exec.Command(execCmd, execArgs...)
			proc.Stdout = os.Stdout
			proc.Stderr = os.Stderr
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
	cmd.Flags().StringVar(&bin, "bin", "go", "executor binary or 'go' for go run")
	cmd.Flags().StringVar(&executorURL, "executor-url", "", "URL of running executor to shutdown (default http://localhost<addr>)")
	return cmd
}

func newRunStartCmd(executorURL *string, output *string) *cobra.Command {
	var percent int
	var baseTPS float64
	var rampUp int
	var variables varsFlag

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start load generation",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := executor.NewClient(*executorURL, 10*time.Second)
			body := map[string]any{
				"percent":          percent,
				"base_tps":         baseTPS,
				"ramp_up_seconds":  rampUp,
				"variables":        map[string]string(variables),
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
	cmd.Flags().Var(&variables, "var", "scenario variable in key=value format (repeatable)")

	return cmd
}

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

func newRunUpdateCmd(executorURL *string, output *string) *cobra.Command {
	var percent int
	var baseTPS float64
	var rampUp int

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update load configuration (percent/base TPS/ramp-up) without restart",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := executor.NewClient(*executorURL, 10*time.Second)
			body := map[string]any{
				"percent":          percent,
				"base_tps":         baseTPS,
				"ramp_up_seconds":  rampUp,
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

	return cmd
}

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

