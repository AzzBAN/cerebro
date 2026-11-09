package app

import (
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/positionproposal"
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

func (m multiSink) SendProposals(p []positionproposal.Proposal) {
	for _, s := range m {
		s.SendProposals(p)
	}
}

func (m multiSink) SendBias(b domain.BiasResult) {
	for _, s := range m {
		s.SendBias(b)
	}
}

// opportunitySink is the optional capability a UI surface may implement to
// receive ranked screening opportunities. The web dashboard implements it;
// the TUI does not. multiSink fans out only to members that satisfy it, so
// the feature is purely additive — no change to the uistate.Sink interface.
type opportunitySink interface {
	SendOpportunities(opps []domain.ScreeningOpportunity)
}

// SendOpportunities forwards the ranked opportunities to every member that
// implements opportunitySink, skipping those (e.g. the TUI) that do not.
func (m multiSink) SendOpportunities(opps []domain.ScreeningOpportunity) {
	for _, s := range m {
		if os, ok := s.(opportunitySink); ok {
			os.SendOpportunities(opps)
		}
	}
}

// accountSink is an optional capability a UI surface may implement to receive
// account balance and margin status.
type accountSink interface {
	SendAccount(a uistate.AccountSnapshot)
}

// SendAccount forwards account snapshots to every member implementing accountSink.
func (m multiSink) SendAccount(a uistate.AccountSnapshot) {
	for _, s := range m {
		if as, ok := s.(accountSink); ok {
			as.SendAccount(a)
		}
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
