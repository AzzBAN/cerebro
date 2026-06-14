package tui

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// clipboardCopiedMsg is delivered after a successful (or attempted) copy so
// the UI can flash a confirmation notice.
type clipboardCopiedMsg struct {
	chars int
	err   error
}

// copyToClipboardCmd writes text to the system clipboard via OSC 52 and a
// platform-native fallback (pbcopy / wl-copy / xclip / xsel). Both paths are
// attempted because OSC 52 works over SSH but is disabled in some terminals
// (Apple Terminal, default tmux), while native tools always work locally but
// not over SSH. Sending both is the most robust combination.
func copyToClipboardCmd(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	return func() tea.Msg {
		// OSC 52: harmless if the terminal doesn't understand it.
		if _, err := os.Stdout.WriteString(osc52Set(text)); err != nil {
			return clipboardCopiedMsg{err: err}
		}
		// Native fallback: best-effort, ignore errors so OSC 52 alone still
		// counts as a successful copy from the user's perspective.
		_ = copyNative(text)
		return clipboardCopiedMsg{chars: utf8.RuneCountInString(text)}
	}
}

// osc52Set builds the OSC 52 escape sequence to set clipboard contents.
// Format: ESC ] 52 ; c ; <base64 payload> BEL
func osc52Set(text string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	var b strings.Builder
	b.Grow(len(enc) + 8)
	b.WriteString("\x1b]52;c;")
	b.WriteString(enc)
	b.WriteByte(0x07)
	return b.String()
}

// copyNative pipes text to a platform clipboard tool. Returns an error if
// no supported tool is available.
func copyNative(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		switch {
		case hasCmd("wl-copy"):
			cmd = exec.Command("wl-copy")
		case hasCmd("xclip"):
			cmd = exec.Command("xclip", "-selection", "clipboard")
		case hasCmd("xsel"):
			cmd = exec.Command("xsel", "--clipboard", "--input")
		default:
			return errors.New("no clipboard tool found (install wl-copy, xclip, or xsel)")
		}
	case "windows":
		cmd = exec.Command("clip")
	default:
		return errors.New("unsupported platform for native clipboard")
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func hasCmd(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
