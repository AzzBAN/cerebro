package chatops

import "testing"

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantCmd string
		wantArg string
	}{
		{"bare command", "/status", CmdStatus, ""},
		{"command with arg", "/ask why did you buy BTC?", CmdAsk, "why did you buy BTC?"},
		{"telegram botname suffix", "/ask@CerebroBot why did you buy BTC?", CmdAsk, "why did you buy BTC?"},
		{"botname suffix no arg", "/status@CerebroBot", CmdStatus, ""},
		// Regression: an "@" inside the free-form /ask argument must NOT
		// truncate the query. The @botname strip applies only to the command
		// token (first whitespace-delimited word).
		{"at-sign in price", "/ask is BTC@60000 a good entry?", CmdAsk, "is BTC@60000 a good entry?"},
		{"at-sign venue", "/ask what is funding on BTC@Binance perp?", CmdAsk, "what is funding on BTC@Binance perp?"},
		{"email in arg", "/ask should I email ops@desk.io about this?", CmdAsk, "should I email ops@desk.io about this?"},
		{"botname plus at-sign arg", "/ask@CerebroBot is BTC@60000 cheap?", CmdAsk, "is BTC@60000 cheap?"},
		{"not a command", "hello there", "", "hello there"},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, arg := ParseCommand(tt.raw)
			if cmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.wantCmd)
			}
			if arg != tt.wantArg {
				t.Errorf("arg = %q, want %q", arg, tt.wantArg)
			}
		})
	}
}
