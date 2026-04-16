package chatops

// Command names — the canonical slash-command identifiers.
const (
	CmdStatus    = "/status"
	CmdPause     = "/pause"
	CmdFlatten   = "/flatten"
	CmdResume    = "/resume"
	CmdBias      = "/bias"
	CmdPositions = "/positions"
	CmdAsk       = "/ask"
)

// Permission describes what a command requires.
type Permission struct {
	RequiresOperator      bool
	RequiresConfirmation  bool
	ConfirmationTimeoutS  int
}

// commandPermissions maps each command to its permission requirements.
var commandPermissions = map[string]Permission{
	CmdStatus:    {RequiresOperator: true},
	CmdPause:     {RequiresOperator: true},
	CmdFlatten:   {RequiresOperator: true, RequiresConfirmation: true, ConfirmationTimeoutS: 30},
	CmdResume:    {RequiresOperator: true},
	CmdBias:      {RequiresOperator: true},
	CmdPositions: {RequiresOperator: true},
	CmdAsk:       {RequiresOperator: true},
}

// ParseCommand extracts the command name and argument from a raw input string.
// "/ask Why did you buy BTC?" → ("/ask", "Why did you buy BTC?")
func ParseCommand(raw string) (cmd, arg string) {
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
