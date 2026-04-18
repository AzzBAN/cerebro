package tools

import (
	"fmt"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/port"
)

// Registry maps tool names to their implementations, enforcing per-agent deny lists.
type Registry struct {
	all    map[string]port.Tool
	policy config.ToolPolicyConfig
}

// NewRegistry creates an empty registry.
func NewRegistry(policy config.ToolPolicyConfig) *Registry {
	return &Registry{
		all:    make(map[string]port.Tool),
		policy: policy,
	}
}

// Register adds a tool. Panics on duplicate names.
func (r *Registry) Register(name string, tool port.Tool) {
	if _, exists := r.all[name]; exists {
		panic(fmt.Sprintf("tool registry: duplicate tool %q", name))
	}
	r.all[name] = tool
}

func (r *Registry) deniedForAgent(agentName string) []string {
	switch agentName {
	case "copilot":
		return r.policy.Copilot.Denied
	case "screening":
		return r.policy.Screening.Denied
	case "risk":
		return r.policy.Risk.Denied
	case "reviewer":
		return r.policy.Reviewer.Denied
	default:
		return nil
	}
}

// ForAgent returns the tool set visible to the given agent role,
// with denied tools removed according to the tool policy table.
// Returns a map of tool name -> handler for backward compatibility.
func (r *Registry) ForAgent(agentName string) map[string]port.ToolHandler {
	deniedSet := r.buildDeniedSet(agentName)

	out := make(map[string]port.ToolHandler, len(r.all))
	for name, tool := range r.all {
		if !deniedSet[name] {
			out[name] = tool.Handler
		}
	}
	return out
}

// ForAgentWithDefs returns the full tool set with definitions for the given agent role.
func (r *Registry) ForAgentWithDefs(agentName string) map[string]port.Tool {
	deniedSet := r.buildDeniedSet(agentName)

	out := make(map[string]port.Tool, len(r.all))
	for name, tool := range r.all {
		if !deniedSet[name] {
			out[name] = tool
		}
	}
	return out
}

func (r *Registry) buildDeniedSet(agentName string) map[string]bool {
	denied := r.deniedForAgent(agentName)
	set := make(map[string]bool, len(denied))
	for _, d := range denied {
		set[d] = true
	}
	return set
}
