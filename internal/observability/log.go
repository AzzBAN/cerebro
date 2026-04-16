package observability

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// contextKey is the private key type for context values managed by this package.
type contextKey string

const correlationIDKey contextKey = "correlation_id"

// LogSink is the optional secondary output for the pretty handler.
// The TUI runner implements this to receive formatted log lines.
type LogSink interface {
	// SendSysLog delivers a formatted log line and its level string
	// ("ERROR", "WARN", "INFO", "DEBUG") to the TUI panel.
	SendSysLog(level, line string)
}

// activeHandler holds a reference to the active pretty handler so that
// SetLogSink can attach the TUI runner after startup.
var activeHandler *prettyHandler

// Setup initialises the global slog logger.
//   - Output always goes to os.Stderr so it never conflicts with Bubble Tea
//     which owns stdout in alt-screen mode.
//   - format "json" → machine-readable JSON on stderr (useful for prod / CI).
//   - Any other value → pretty colored text (default for interactive use).
//
// Call SetLogSink after the TUI runner is created to forward log lines to the
// TUI panel.
func Setup(level, format string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h := newPrettyHandler(os.Stderr, opts, nil)
		activeHandler = h
		handler = h
	}
	slog.SetDefault(slog.New(handler))
}

// SetLogSink attaches a secondary output (e.g. the TUI runner) to the active
// pretty handler. Every subsequent log line is also forwarded to sink.
// Safe to call from any goroutine; no-op if Setup used JSON format.
func SetLogSink(sink LogSink) {
	if activeHandler == nil {
		return
	}
	activeHandler.mu.Lock()
	activeHandler.sink = sink
	activeHandler.mu.Unlock()
}

// WithCorrelationID returns a context carrying the given correlation ID.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationID extracts the correlation ID from the context.
func CorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(correlationIDKey).(string); ok {
		return id
	}
	return ""
}

// FromContext returns a logger pre-populated with the correlation_id if present.
func FromContext(ctx context.Context) *slog.Logger {
	if id := CorrelationID(ctx); id != "" {
		return slog.With("correlation_id", id)
	}
	return slog.Default()
}

// ─── Pretty handler ───────────────────────────────────────────────────────────

// ANSI escape codes.
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiGray   = "\033[90m"
	ansiCyan   = "\033[36m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiBoldRed = "\033[1;31m"
	ansiGreen  = "\033[32m"
)

// levelTag maps a slog.Level to a short coloured tag.
func levelTag(l slog.Level) (tag, color string) {
	switch {
	case l >= slog.LevelError:
		return "ERR", ansiBoldRed
	case l >= slog.LevelWarn:
		return "WRN", ansiYellow
	case l >= slog.LevelInfo:
		return "INF", ansiCyan
	default:
		return "DBG", ansiGray
	}
}

// prettyHandler writes compact, human-readable log lines to w and optionally
// forwards them to a LogSink (TUI panel). It is intentionally minimal —
// groups are flattened and attrs are rendered inline.
type prettyHandler struct {
	mu   sync.Mutex
	w    io.Writer
	opts *slog.HandlerOptions
	sink LogSink

	// pre-formatted attribute prefix from WithAttrs / WithGroup calls.
	prefix string
}

func newPrettyHandler(w io.Writer, opts *slog.HandlerOptions, sink LogSink) *prettyHandler {
	return &prettyHandler{w: w, opts: opts, sink: sink}
}

func (h *prettyHandler) clone() *prettyHandler {
	return &prettyHandler{
		w:      h.w,
		opts:   h.opts,
		sink:   h.sink,
		prefix: h.prefix,
	}
}

func (h *prettyHandler) Enabled(_ context.Context, l slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts != nil && h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return l >= minLevel
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	tag, color := levelTag(r.Level)
	ts := r.Time.Format("15:04:05")

	// Build attrs string.
	var attrsBuf bytes.Buffer
	r.Attrs(func(a slog.Attr) bool {
		attrsBuf.WriteByte(' ')
		attrsBuf.WriteString(ansiGray)
		attrsBuf.WriteString(a.Key)
		attrsBuf.WriteString("=")
		attrsBuf.WriteString(ansiReset)
		v := a.Value.String()
		if strings.ContainsAny(v, " \t\n") {
			fmt.Fprintf(&attrsBuf, "%q", v)
		} else {
			attrsBuf.WriteString(v)
		}
		return true
	})
	attrs := attrsBuf.String()
	if h.prefix != "" {
		attrs = " " + h.prefix + attrs
	}

	// Plain line for the sink (no ANSI). Keep this message-only because the TUI
	// log renderer already prepends timestamp + level badge.
	plainLine := fmt.Sprintf("%s%s", r.Message, stripANSI(attrs))

	// Coloured line for the terminal.
	line := fmt.Sprintf("%s%s%s %s%s%s %s%s%s%s\n",
		ansiGray, ts, ansiReset,
		color, tag, ansiReset,
		ansiBold, r.Message, ansiReset,
		attrs,
	)

	h.mu.Lock()
	_, err := io.WriteString(h.w, line)
	h.mu.Unlock()

	if h.sink != nil {
		levelStr := r.Level.String()
		h.sink.SendSysLog(levelStr, plainLine)
	}
	return err
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := h.clone()
	var sb strings.Builder
	for _, a := range attrs {
		sb.WriteByte(' ')
		sb.WriteString(a.Key)
		sb.WriteByte('=')
		sb.WriteString(a.Value.String())
	}
	c.prefix += sb.String()
	return c
}

func (h *prettyHandler) WithGroup(name string) slog.Handler {
	c := h.clone()
	c.prefix += name + "."
	return c
}

// stripANSI removes ANSI escape sequences from s.
func stripANSI(s string) string {
	var out bytes.Buffer
	i := 0
	for i < len(s) {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // skip 'm'
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// ─── Heartbeat formatter ──────────────────────────────────────────────────────

// HeartbeatFields holds the values needed to render a heartbeat summary.
type HeartbeatFields struct {
	TradingState         string
	Halted               bool
	OpenPositions        int
	CandlesProduced      int64
	CandlesStrategy      int64
	CandlesFiller        int64
	SignalsFired         int64
	SignalsDeduped       int64
	SignalsRiskRejected  int64
	OrdersRouted         int64
	OrderErrors          int64
	Timestamp            time.Time
}

// FormatHeartbeat returns a compact multi-line heartbeat block.
// When sink is non-nil it also pushes it to the TUI panel.
func FormatHeartbeat(f HeartbeatFields, sink LogSink) string {
	stateColor := ansiGreen
	if f.Halted {
		stateColor = ansiRed
	}

	ts := f.Timestamp.Format("15:04:05")

	line1 := fmt.Sprintf("%s%s%s %s♥ HEARTBEAT%s  state=%s%s%s  halted=%v  positions=%d",
		ansiGray, ts, ansiReset,
		ansiCyan, ansiReset,
		stateColor, f.TradingState, ansiReset,
		f.Halted, f.OpenPositions,
	)
	line2 := fmt.Sprintf("         candles: produced=%-4d  strategy=%-4d  filler=%-4d",
		f.CandlesProduced, f.CandlesStrategy, f.CandlesFiller)
	line3 := fmt.Sprintf("         signals: fired=%-4d  deduped=%-4d  risk_rejected=%-4d",
		f.SignalsFired, f.SignalsDeduped, f.SignalsRiskRejected)
	line4 := fmt.Sprintf("         orders:  routed=%-4d  errors=%-4d",
		f.OrdersRouted, f.OrderErrors)

	block := strings.Join([]string{line1, line2, line3, line4}, "\n") + "\n"

	if sink != nil {
		plain := fmt.Sprintf("♥ HEARTBEAT state=%s halted=%v positions=%d | candles prod=%d strat=%d | signals fired=%d rejected=%d | orders routed=%d errors=%d",
			f.TradingState, f.Halted, f.OpenPositions,
			f.CandlesProduced, f.CandlesStrategy,
			f.SignalsFired, f.SignalsRiskRejected,
			f.OrdersRouted, f.OrderErrors,
		)
		sink.SendSysLog("INFO", plain)
	}
	return block
}
