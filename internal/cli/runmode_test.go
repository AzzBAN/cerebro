package cli

import (
	"testing"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
)

func TestParseRunMode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    domain.RunMode
		wantErr bool
	}{
		{"canonical tui", "tui", domain.RunModeTUI, false},
		{"canonical web", "web", domain.RunModeWeb, false},
		{"terminal alias", "terminal", domain.RunModeTUI, false},
		{"uppercase", "WEB", domain.RunModeWeb, false},
		{"whitespace padded", "  tui  ", domain.RunModeTUI, false},
		{"empty", "", "", true},
		{"garbage", "banana", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRunMode(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseRunMode(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseRunMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDefaultRunMode(t *testing.T) {
	tests := []struct {
		name string
		mode domain.RunMode
		want domain.RunMode
	}{
		{"explicit tui", domain.RunModeTUI, domain.RunModeTUI},
		{"explicit web", domain.RunModeWeb, domain.RunModeWeb},
		{"empty falls back to tui", "", domain.RunModeTUI},
		{"garbage falls back to tui", "nonsense", domain.RunModeTUI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Run: config.RunConfig{DefaultMode: tt.mode}}
			if got := defaultRunMode(cfg); got != tt.want {
				t.Errorf("defaultRunMode(%q) = %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

// stubMenu returns a menuFunc that always yields the given choice, and records
// whether it was invoked so tests can assert the menu was/ wasn't reached.
func stubMenu(choice runChoice, called *bool) menuFunc {
	return func(domain.RunMode) (runChoice, error) {
		if called != nil {
			*called = true
		}
		return choice, nil
	}
}

func TestResolveStartup(t *testing.T) {
	withCfg := func(def domain.RunMode, menu bool) *config.Config {
		return &config.Config{Run: config.RunConfig{DefaultMode: def, Menu: &menu}}
	}

	tests := []struct {
		name        string
		cliMode     string
		background  bool
		cfg         *config.Config
		noMenu      bool
		menuChoice  runChoice
		wantMode    domain.RunMode
		wantBg      bool
		wantCancel  bool
		wantErr     bool
		wantMenuRun bool
	}{
		{
			name:       "background flag wins, no menu",
			background: true,
			cfg:        withCfg(domain.RunModeTUI, true),
			wantMode:   domain.RunModeWeb,
			wantBg:     true,
		},
		{
			name:     "mode flag overrides default, skips menu",
			cliMode:  "web",
			cfg:      withCfg(domain.RunModeTUI, true),
			wantMode: domain.RunModeWeb,
		},
		{
			name:    "invalid mode flag errors",
			cliMode: "bogus",
			cfg:     withCfg(domain.RunModeTUI, true),
			wantErr: true,
		},
		{
			name:     "no-menu uses configured default",
			cfg:      withCfg(domain.RunModeWeb, true),
			noMenu:   true,
			wantMode: domain.RunModeWeb,
		},
		{
			name:     "menu disabled in config uses default",
			cfg:      withCfg(domain.RunModeWeb, false),
			wantMode: domain.RunModeWeb,
		},
		{
			name:     "empty default falls back to tui",
			cfg:      withCfg("", true),
			noMenu:   true,
			wantMode: domain.RunModeTUI,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var menuRan bool
			got, err := resolveStartup(tt.cliMode, tt.background, tt.cfg, tt.noMenu, stubMenu(tt.menuChoice, &menuRan))
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveStartup err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", got.mode, tt.wantMode)
			}
			if got.background != tt.wantBg {
				t.Errorf("background = %v, want %v", got.background, tt.wantBg)
			}
			if got.cancelled != tt.wantCancel {
				t.Errorf("cancelled = %v, want %v", got.cancelled, tt.wantCancel)
			}
			if menuRan != tt.wantMenuRun {
				t.Errorf("menu invoked = %v, want %v", menuRan, tt.wantMenuRun)
			}
		})
	}
}

// TestResolveStartupMenuChoices verifies each menu choice maps to the right
// decision. These force the menu path by enabling it in config; whether a TTY
// is present is irrelevant because the stub menu replaces the real widget —
// but resolveStartup still gates on hasTTY(), so we only assert the mapping
// when a TTY is actually available (interactive dev), and skip otherwise.
func TestResolveStartupMenuChoices(t *testing.T) {
	if !hasTTY() {
		t.Skip("no TTY: menu branch is unreachable in this environment")
	}
	menu := true
	cfg := &config.Config{Run: config.RunConfig{DefaultMode: domain.RunModeTUI, Menu: &menu}}

	cases := []struct {
		choice     runChoice
		wantMode   domain.RunMode
		wantBg     bool
		wantCancel bool
	}{
		{choiceTUI, domain.RunModeTUI, false, false},
		{choiceWeb, domain.RunModeWeb, false, false},
		{choiceBackground, domain.RunModeWeb, true, false},
		{choiceQuit, "", false, true},
	}
	for _, c := range cases {
		t.Run(string(c.choice), func(t *testing.T) {
			got, err := resolveStartup("", false, cfg, false, stubMenu(c.choice, nil))
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.mode != c.wantMode || got.background != c.wantBg || got.cancelled != c.wantCancel {
				t.Errorf("choice %q → %+v, want mode=%q bg=%v cancel=%v",
					c.choice, got, c.wantMode, c.wantBg, c.wantCancel)
			}
		})
	}
}
