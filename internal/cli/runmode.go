package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
)

// runChoice is what the operator picks at the interactive startup menu. It is
// broader than domain.RunMode because the menu also offers actions (detach to
// background, quit) that are not terminal-presentation modes.
type runChoice string

const (
	choiceTUI        runChoice = "tui"        // foreground rich TUI + web dashboard
	choiceWeb        runChoice = "web"        // foreground logs + web dashboard
	choiceBackground runChoice = "background" // detach headless, parent exits
	choiceQuit       runChoice = "quit"       // abort startup
)

// startupDecision is the resolved outcome of flag + menu + config precedence.
type startupDecision struct {
	// mode is the terminal presentation to run with (ignored when cancelled).
	mode domain.RunMode
	// background, when true, means re-exec the engine detached and exit the
	// parent. The detached child always runs in RunModeWeb.
	background bool
	// cancelled, when true, means the operator quit the menu — do nothing.
	cancelled bool
}

// menuFunc renders the interactive picker and returns the operator's choice.
// It is injected so resolveStartup stays unit-testable without a real TTY.
type menuFunc func(def domain.RunMode) (runChoice, error)

// resolveStartup decides what to do at startup using this precedence:
//
//  1. --background flag → detach (headless web), regardless of TTY;
//  2. --mode flag → that mode, foreground, no menu;
//  3. interactive menu, when a TTY is attached, run.menu is on, and --no-menu
//     was not passed;
//  4. run.default_mode from app.yaml (falling back to "tui").
//
// On a headless host (VPS / systemd / docker / nohup) steps 1–2 are skipped
// unless flagged, the menu in step 3 is unreachable (no TTY), and resolution
// falls through to the configured default — what a detached process wants.
func resolveStartup(cliMode string, background bool, cfg *config.Config, noMenu bool, menu menuFunc) (startupDecision, error) {
	if background {
		return startupDecision{mode: domain.RunModeWeb, background: true}, nil
	}

	if cliMode != "" {
		m, err := parseRunMode(cliMode)
		if err != nil {
			return startupDecision{}, err
		}
		return startupDecision{mode: m}, nil
	}

	def := defaultRunMode(cfg)

	if cfg.Run.ShowMenu() && !noMenu && hasTTY() {
		choice, err := menu(def)
		if err != nil {
			return startupDecision{}, fmt.Errorf("run-mode menu: %w", err)
		}
		switch choice {
		case choiceTUI:
			return startupDecision{mode: domain.RunModeTUI}, nil
		case choiceWeb:
			return startupDecision{mode: domain.RunModeWeb}, nil
		case choiceBackground:
			return startupDecision{mode: domain.RunModeWeb, background: true}, nil
		case choiceQuit:
			return startupDecision{cancelled: true}, nil
		default:
			return startupDecision{mode: def}, nil
		}
	}

	return startupDecision{mode: def}, nil
}

// defaultRunMode returns the configured default, falling back to TUI when
// run.default_mode is unset or unrecognised.
func defaultRunMode(cfg *config.Config) domain.RunMode {
	if cfg.Run.DefaultMode == domain.RunModeTUI || cfg.Run.DefaultMode == domain.RunModeWeb {
		return cfg.Run.DefaultMode
	}
	return domain.RunModeTUI
}

// parseRunMode normalises the --mode flag value into a RunMode.
func parseRunMode(s string) (domain.RunMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tui", "terminal":
		return domain.RunModeTUI, nil
	case "web":
		return domain.RunModeWeb, nil
	default:
		return "", fmt.Errorf("invalid run mode %q: must be \"tui\" or \"web\"", s)
	}
}

// hasTTY reports whether an interactive terminal is attached. It mirrors the
// runtime's own check so the menu and the engine agree on headlessness.
func hasTTY() bool {
	f, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
