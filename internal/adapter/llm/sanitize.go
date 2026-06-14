package llm

import (
	"regexp"
	"strings"
)

// reasoningTagRe matches reasoning/thinking blocks that some models — notably
// reasoning models routed through OpenAI-compatible proxies (e.g. OpenRouter)
// — emit inline in their message content instead of in a dedicated
// `reasoning_content` field. We strip these so the operator never sees the
// model's chain-of-thought leaking into the final answer.
//
// Matches <think>…</think>, <thinking>…</thinking>, and <reasoning>…</reasoning>
// case-insensitively and across newlines. A dangling open tag with no close
// (truncated stream) is handled separately in stripReasoning.
var reasoningTagRe = regexp.MustCompile(`(?is)<(think|thinking|reasoning)>.*?</(think|thinking|reasoning)>`)

// danglingOpenRe matches an unterminated reasoning open tag and everything
// after it — covers the case where the model opened a <think> block but the
// turn ended (or was truncated) before the closing tag.
var danglingOpenRe = regexp.MustCompile(`(?is)<(think|thinking|reasoning)>.*$`)

// stripReasoning removes inline reasoning/thinking blocks from an LLM's final
// text output and trims surrounding whitespace. It is applied at the point
// each adapter returns its final answer, so every agent (copilot, screening,
// reviewer) is protected regardless of which provider served the call.
//
// It is deliberately conservative: it only removes well-formed reasoning tags
// (or a single dangling open tag), never arbitrary angle-bracket content, so
// legitimate text containing other XML/HTML-looking fragments is preserved.
func stripReasoning(s string) string {
	if s == "" {
		return s
	}
	out := reasoningTagRe.ReplaceAllString(s, "")
	// Clean up a dangling/truncated open tag if one survived (no matching close).
	if strings.Contains(strings.ToLower(out), "<think") ||
		strings.Contains(strings.ToLower(out), "<reasoning") {
		out = danglingOpenRe.ReplaceAllString(out, "")
	}
	return strings.TrimSpace(out)
}
