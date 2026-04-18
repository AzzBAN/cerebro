package tui

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/charmbracelet/bubbletea"
)

// Runner starts the Bubble Tea program and feeds market data events into it.
// Create via NewRunner, then call Push() from any goroutine to inject events
// into the log panel. Run() must be called in its own goroutine.
//
// Runner also implements observability.LogSink — pass it to observability.Setup
// so that every slog line is forwarded to the TUI system-log panel.
type Runner struct {
	hub  *marketdata.Hub
	prog *tea.Program
	msgCh chan tea.Msg
	model *Model
}

// NewRunner creates a TUI Runner and initialises the Bubble Tea program.
// The program is ready to receive Push() calls immediately; Run() starts
// the actual rendering loop.
func NewRunner(hub *marketdata.Hub, maxLogLines int) *Runner {
	m := New(maxLogLines)
	// Bubble Tea owns stdout for alt-screen rendering.
	// slog must be directed to stderr (done by observability.Setup) to avoid
	// interleaving with the TUI output.
	p := tea.NewProgram(&m, tea.WithAltScreen())
	r := &Runner{
		hub:   hub,
		prog:  p,
		msgCh: make(chan tea.Msg, 512),
		model: &m,
	}

	// Decouple producers from Bubble Tea internals so startup logs and bursty
	// events never block runtime wiring or shutdown.
	go func() {
		for msg := range r.msgCh {
			r.prog.Send(msg)
		}
	}()

	return r
}

// Push sends any tea.Msg to the running program.
// Safe to call from any goroutine before or after Run() starts.
func (r *Runner) Push(msg tea.Msg) {
	select {
	case r.msgCh <- msg:
	default:
		// Drop on saturation to preserve liveness. Quotes/logs are ephemeral.
	}
}

// Run starts the TUI rendering loop. Blocks until the user quits (q / Ctrl-C / Esc)
// or ctx is cancelled. Forwards hub quote events to the ticker panel.
func (r *Runner) Run(ctx context.Context) error {
	quotes, candles := r.hub.Subscribe()

	go func() {
		for {
			select {
			case <-ctx.Done():
				r.prog.Quit()
				return
			case evt, ok := <-quotes:
				if !ok {
					return
				}
				r.Push(QuoteMsg(evt))
			case _, ok := <-candles:
				if !ok {
					return
				}
				// Candles drive strategy engine; TUI does not display raw candle data.
			}
		}
	}()

	if _, err := r.prog.Run(); err != nil {
		slog.Error("TUI error", "error", err)
		return err
	}
	return nil
}

// SendAgentLog delivers an agent reasoning line to the TUI agent-log panel.
func (r *Runner) SendAgentLog(line string) {
	r.Push(AgentLogMsg{Line: line})
}

// SendSysLog implements observability.LogSink.
// It delivers a system log line (from slog) to the TUI log panel with the
// appropriate level label so it can be coloured differently from agent output.
func (r *Runner) SendSysLog(level, line string) {
	r.Push(SysLogMsg{Level: level, Line: line, At: time.Now()})
}

// SendHeartbeat pushes a formatted heartbeat summary to the TUI status bar.
func (r *Runner) SendHeartbeat(line string) {
	r.Push(HeartbeatMsg{Line: line, At: time.Now()})
}

// SendPositions pushes an open-position snapshot to the TUI positions panel.
func (r *Runner) SendPositions(positions []domain.Position) {
	r.Push(PositionsMsg{Positions: positions})
}

// SendAgentState pushes a live agent step transition to the TUI agent panel.
func (r *Runner) SendAgentState(msg AgentStateMsg) {
	r.Push(msg)
}

// SetCopilotFn injects the copilot ask function into the TUI model.
func (r *Runner) SetCopilotFn(fn func(ctx context.Context, query string) (string, error)) {
	r.model.SetCopilotFn(fn)
}

// formatAgentLine formats an agent log entry with a visual prefix.
func formatAgentLine(line string) string {
	return fmt.Sprintf("🤖 %s", line)
}
