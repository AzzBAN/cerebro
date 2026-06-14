package app

import (
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/uistate"
)

// multiSink fans every UI event out to all registered live surfaces (the
// Bubble Tea TUI and/or the web dashboard). It is composition-root glue: the
// engine emits one event and it appears on every surface.
//
// An empty multiSink is a valid no-op sink, so call sites never need nil
// checks. Construct via newMultiSink, which drops nil members — important
// because a typed-nil *tui.Runner wrapped in a uistate.Sink interface is not
// itself nil and would panic when its methods ran on a nil receiver.
type multiSink []uistate.Sink

func newMultiSink(sinks ...uistate.Sink) multiSink {
	out := make(multiSink, 0, len(sinks))
	for _, s := range sinks {
		if s != nil {
			out = append(out, s)
		}
	}
	return out
}

func (m multiSink) SendPositions(p []domain.Position) {
	for _, s := range m {
		s.SendPositions(p)
	}
}

func (m multiSink) SendBias(b domain.BiasResult) {
	for _, s := range m {
		s.SendBias(b)
	}
}

func (m multiSink) SendMacro(s uistate.MacroSnapshot) {
	for _, sink := range m {
		sink.SendMacro(s)
	}
}

func (m multiSink) SendNews(n uistate.NewsSnapshot) {
	for _, s := range m {
		s.SendNews(n)
	}
}

func (m multiSink) SendBudget(b uistate.BudgetSnapshot) {
	for _, s := range m {
		s.SendBudget(b)
	}
}

func (m multiSink) SendHeartbeat(line string) {
	for _, s := range m {
		s.SendHeartbeat(line)
	}
}

func (m multiSink) SendAgentState(st uistate.AgentState) {
	for _, s := range m {
		s.SendAgentState(st)
	}
}

func (m multiSink) SendAgentLog(line string) {
	for _, s := range m {
		s.SendAgentLog(line)
	}
}

func (m multiSink) SendOrderLog(line string) {
	for _, s := range m {
		s.SendOrderLog(line)
	}
}

func (m multiSink) SendSysLog(level, line string) {
	for _, s := range m {
		s.SendSysLog(level, line)
	}
}
