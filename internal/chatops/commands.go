package chatops

import "strings"

// Command names — the canonical slash-command identifiers.
const (
	CmdStatus    = "/status"
	CmdPause     = "/pause"
	CmdFlatten   = "/flatten"
	CmdResume    = "/resume"
	CmdBias      = "/bias"
	CmdPositions = "/positions"
	CmdClose     = "/close"
	CmdConfirm   = "/confirm"
	CmdReject    = "/reject"
	CmdAsk       = "/ask"
	CmdSummary   = "/summary"
	CmdHelp      = "/help"
)

// Permission describes what a command requires.
type Permission struct {
	RequiresOperator     bool
	RequiresConfirmation bool
	ConfirmationTimeoutS int
}

// commandPermissions maps each command to its permission requirements.
var commandPermissions = map[string]Permission{
	CmdStatus:    {RequiresOperator: true},
	CmdPause:     {RequiresOperator: true},
	CmdFlatten:   {RequiresOperator: true, RequiresConfirmation: true, ConfirmationTimeoutS: 30},
	CmdResume:    {RequiresOperator: true},
	CmdBias:      {RequiresOperator: true},
	CmdPositions: {RequiresOperator: true},
	CmdClose:     {RequiresOperator: true, RequiresConfirmation: true, ConfirmationTimeoutS: 30},
	CmdConfirm:   {RequiresOperator: true},
	CmdReject:    {RequiresOperator: true},
	CmdAsk:       {RequiresOperator: true},
	CmdSummary:   {RequiresOperator: true},
	CmdHelp:      {RequiresOperator: true},
}

// ParseCommand extracts the command name and argument from a raw input string.
// "/ask Why did you buy BTC?" → ("/ask", "Why did you buy BTC?")
// Handles Telegram's "/cmd@BotName" format by stripping the @ suffix.
func ParseCommand(raw string) (cmd, arg string) {
	// Strip Telegram's "@botname" suffix, which only ever appears inside the
	// command token itself (e.g. "/ask@CerebroBot ..."), never in the free-form
	// argument. Bound the search to the first whitespace-delimited token so an
	// "@" inside an /ask question (e.g. "/ask is BTC@60000 a good entry?" or
	// "/ask email ops@desk.io") is preserved rather than truncating the query.
	if raw != "" && raw[0] == '/' {
		head, tail := raw, ""
		if sp := strings.IndexByte(raw, ' '); sp >= 0 {
			head, tail = raw[:sp], raw[sp:] // tail keeps its leading space
		}
		if at := strings.IndexByte(head, '@'); at > 0 {
			head = head[:at]
		}
		raw = head + tail
	}
	for name := range commandPermissions {
		if len(raw) >= len(name) && raw[:len(name)] == name {
			rest := raw[len(name):]
			if len(rest) > 0 && rest[0] == ' ' {
				rest = rest[1:]
			}
			return name, rest
		}
	}
	return "", raw
}
