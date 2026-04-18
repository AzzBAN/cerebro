---
name: test
description: "Run tests using the senior-go-engineer agent. Usage: /test [package or file]"
---

# Test Skill

Launches the **senior-go-engineer** agent to run and validate tests, and write missing tests if needed.

## Instructions

When the user invokes `/test`, follow these steps:

1. Determine the test target from the user's arguments:
   - If a package path is given (e.g., `/test internal/risk/`), run tests for that package.
   - If a file is given (e.g., `/test internal/risk/gate.go`), run tests for the package containing that file.
   - If "all" or no argument is given, run `go test ./...`.

2. Launch the **senior-go-engineer** agent with a prompt containing:
   - The specific test command to run
   - Instructions to analyze test output, identify failures, and suggest fixes
   - If tests are missing, propose table-driven test cases following project conventions
   - Run `go vet` and `golangci-lint` as quality gates

3. Present the agent's findings and any suggested fixes to the user.

## Examples

- `/test` — run all tests
- `/test internal/risk/` — test the risk package
- `/test internal/execution/monitor.go` — test the package containing monitor.go
- `/test --race` — run all tests with race detector
