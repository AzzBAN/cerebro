package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// detachBackground re-executes the engine as a detached child process and
// returns nil so the parent (this process) exits cleanly, freeing the
// terminal. The child runs headless in web mode, in its own session (setsid),
// with stdout/stderr redirected to a log file so output survives the parent's
// exit and the SSH session closing.
//
// The child is invoked with the original environment flag plus --mode web and
// --no-menu so it never tries to draw a TUI or re-prompt. A CEREBRO_DETACHED
// guard env var prevents any accidental recursion.
func detachBackground(cfgDir string, env domain.Environment) error {
	// Guard against re-entrancy: a detached child must never detach again.
	if os.Getenv("CEREBRO_DETACHED") == "1" {
		return fmt.Errorf("already running detached; refusing to re-detach")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	logPath, err := detachLogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open background log %s: %w", logPath, err)
	}
	defer logFile.Close()

	args := []string{"run", "--" + string(envFlag(env)), "--mode", "web", "--no-menu", "--config-dir", cfgDir}

	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "CEREBRO_DETACHED=1")
	// Setsid detaches the child from the controlling terminal so it keeps
	// running after the parent exits and the SSH session closes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detached engine: %w", err)
	}

	pid := cmd.Process.Pid
	// Release so the parent doesn't hold a wait handle on the child.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release detached process: %w", err)
	}

	fmt.Printf("cerebro started in background (env=%s, mode=web)\n", env)
	fmt.Printf("  pid:  %d\n", pid)
	fmt.Printf("  logs: %s\n", logPath)
	fmt.Printf("  tail: tail -f %s\n", logPath)
	fmt.Printf("  stop: kill %d\n", pid)
	return nil
}

// envFlag maps an Environment to the run subcommand flag that selects it.
func envFlag(env domain.Environment) domain.Environment {
	switch env {
	case domain.EnvironmentDemo:
		return domain.EnvironmentDemo
	case domain.EnvironmentLive:
		return domain.EnvironmentLive
	default:
		return domain.EnvironmentPaper
	}
}

// detachLogPath returns the file the detached child writes its output to. It
// prefers ./logs (created if missing) and falls back to the OS temp dir.
func detachLogPath() (string, error) {
	stamp := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("cerebro-%s.log", stamp)

	dir := "logs"
	if err := os.MkdirAll(dir, 0o755); err == nil {
		if abs, aerr := filepath.Abs(filepath.Join(dir, name)); aerr == nil {
			return abs, nil
		}
		return filepath.Join(dir, name), nil
	}
	return filepath.Join(os.TempDir(), name), nil
}
