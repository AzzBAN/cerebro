---
name: fix
description: "Fix critical issues using the senior-go-engineer agent. Usage: /fix [issue or finding]"
---

# Fix Skill

Launches the **senior-go-engineer** agent to fix specific code issues. The agent will propose fixes but requires user confirmation before applying edits.

## Instructions

When the user invokes `/fix`, follow these steps:

1. Determine what to fix from the user's arguments:
   - If a specific finding is referenced (e.g., `/fix float64 in order.go`), fix that specific issue.
   - If a severity level is given (e.g., `/fix critical`), fix all critical findings from the most recent review.
   - If "all" or no argument is given, present a prioritized list of known issues and ask which to fix.

2. Launch the **senior-go-engineer** agent with a prompt containing:
   - The specific issues to fix with file paths
   - Instructions to follow all project rules (hexagonal architecture, decimal for money, slog for logging, etc.)
   - Requirement to run `go test` and `go vet` after applying fixes
   - Reminder that all Edit/Write operations require user confirmation

3. Present the agent's proposed changes to the user for approval before applying.

## Examples

- `/fix float64 in order.go` — fix a specific issue
- `/fix critical` — fix all critical findings
- `/fix risk gate no-op checks` — fix the dead code in risk gate
- `/fix all` — show prioritized issue list and fix
