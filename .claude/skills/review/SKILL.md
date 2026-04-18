---
name: review
description: "Review code using the senior-go-engineer agent. Usage: /review [path or description]"
---

# Review Skill

Launches the **senior-go-engineer** agent to review code for architectural compliance, error handling, concurrency safety, decimal handling, and test coverage.

## Instructions

When the user invokes `/review`, follow these steps:

1. Determine the review target from the user's arguments:
   - If a file path is given (e.g., `/review internal/risk/gate.go`), review that specific file.
   - If a package is given (e.g., `/review internal/execution`), review all files in that package.
   - If a description is given (e.g., `/review recent changes`), use `git diff` to identify changed files and review those.
   - If no argument is given (e.g., `/review`), review all unstaged/uncommitted changes using `git status` and `git diff`.

2. Launch the **senior-go-engineer** agent with a prompt containing:
   - The specific files or scope to review
   - Instructions to check: architecture compliance, error handling, concurrency safety, decimal handling, Go idioms, security, and testing
   - Request findings organized by severity (critical, important, minor) with file:line references

3. Present the agent's findings to the user.

## Examples

- `/review` — review all uncommitted changes
- `/review internal/risk/gate.go` — review a specific file
- `/review internal/agent/` — review the agent package
- `/review recent commits` — review the last few commits
- `/review PR #42` — review a specific pull request using `gh pr diff`
