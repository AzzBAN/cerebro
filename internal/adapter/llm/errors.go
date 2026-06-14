package llm

import "errors"

// ErrLLMCall is returned when an LLM API call fails. Callers wrap this
// so the risk gate can fail closed on errors.Is(err, domain.ErrAgentTimeout).
// ErrLLMCall is also used by the agent runtime to classify a failure as a
// transient (retryable) LLM-side error, distinct from ErrCircuitOpen which
// must NOT be retried.
var ErrLLMCall = errors.New("LLM API call failed")
