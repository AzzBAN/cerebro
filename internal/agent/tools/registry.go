package tools

import (
	"fmt"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/port"
)

// Registry maps tool names to their handlers, enforcing per-agent deny lists.
type Registry struct {
	all    map[string]port.ToolHandler
	policy config.ToolPolicyConfig
}

// NewRegistry creates an empty registry.
func NewRegistry(policy config.ToolPolicyConfig) *Registry {
	return &Registry{
		all:    make(map[string]port.ToolHandler),
		policy: policy,
	}
}

// Register adds a tool. Panics on duplicate names.
func (r *Registry) Register(name string, handler port.ToolHandler) {
	if _, exists := r.all[name]; exists {
		panic(fmt.Sprintf("tool registry: duplicate tool %q", name))
	}
	r.all[name] = handler
}

// ForAgent returns the tool set visible to the given agent role,
// with denied tools removed according to the tool policy table.
func (r *Registry) ForAgent(agentName string) map[string]port.ToolHandler {
	var denied []string
	switch agentName {
	case "copilot":
		denied = r.policy.Copilot.Denied
	case "screening":
		denied = r.policy.Screening.Denied
	}

	deniedSet := make(map[string]bool, len(denied))
	for _, d := range denied {
		deniedSet[d] = true
	}

	out := make(map[string]port.ToolHandler, len(r.all))
	for name, handler := range r.all {
		if !deniedSet[name] {
			out[name] = handler
		}
	}
	return out
}
