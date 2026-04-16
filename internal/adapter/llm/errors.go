package llm

import "errors"

// ErrLLMCall is returned when an LLM API call fails. Callers wrap this
// so the risk gate can fail closed on errors.Is(err, domain.ErrAgentTimeout).
var ErrLLMCall = errors.New("LLM API call failed")
