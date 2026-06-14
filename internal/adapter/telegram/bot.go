package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/azhar/cerebro/internal/port"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// telegramMaxMessageLen is Telegram's hard limit on a single message body.
// Messages longer than this are rejected, so we split before sending.
const telegramMaxMessageLen = 4096

// slogBotLogger redirects tgbotapi's internal logger output (which by default
// writes to os.Stderr via the standard library `log` package) to our slog
// pipeline. This is critical when the TUI is running in alt-screen mode:
// any direct stderr write would bleed through and corrupt the rendered UI
// (e.g., "Conflict: terminated by other getUpdates request" overwriting
// panels). Calling tgbotapi.SetLogger once installs this globally.
type slogBotLogger struct{}

func (slogBotLogger) Println(v ...interface{}) {
	slog.Warn("telegram: " + strings.TrimSuffix(fmt.Sprintln(v...), "\n"))
}

func (slogBotLogger) Printf(format string, v ...interface{}) {
	slog.Warn("telegram: " + fmt.Sprintf(format, v...))
}

var loggerOnce sync.Once

// installSlogLogger replaces tgbotapi's stderr logger with one that forwards
// to slog. Idempotent and safe to call multiple times.
func installSlogLogger() {
	loggerOnce.Do(func() {
		_ = tgbotapi.SetLogger(slogBotLogger{})
	})
}

// Bot is the Telegram operator interface.
// It receives commands, dispatches them, and sends notifications.
type Bot struct {
	api        *tgbotapi.BotAPI
	allowlist   map[int64]bool
	dispatchFn func(ctx context.Context, actorID, message string) string
	channelIDs  map[string]int64 // NotifyChannel → chat ID
}

// NewBot creates a Telegram Bot.
// allowlistIDs contains the Telegram user IDs permitted to issue commands.
func NewBot(token string, allowlistIDs []int64) (*Bot, error) {
	// Redirect tgbotapi's library-level logger to slog so that errors like
	// "Conflict: terminated by other getUpdates request" do not write to
	// stderr (which would corrupt the TUI's alt-screen output).
	installSlogLogger()

	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram: create bot: %w", err)
	}
	allowlist := make(map[int64]bool, len(allowlistIDs))
	for _, id := range allowlistIDs {
		allowlist[id] = true
	}
	slog.Info("telegram bot created", "username", api.Self.UserName)
	return &Bot{api: api, allowlist: allowlist, channelIDs: make(map[string]int64)}, nil
}

// SetDispatcher wires the ChatOps dispatcher function.
func (b *Bot) SetDispatcher(fn func(ctx context.Context, actorID, message string) string) {
	b.dispatchFn = fn
}

// SetChannel maps a NotifyChannel name to a Telegram chat ID.
func (b *Bot) SetChannel(name string, chatID int64) {
	b.channelIDs[name] = chatID
}

// Run starts the update polling loop. Blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	slog.Info("telegram bot polling started")
	cfg := tgbotapi.NewUpdate(0)
	cfg.Timeout = 60
	updates := b.api.GetUpdatesChan(cfg)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			slog.Info("telegram bot stopping")
			return nil
		case update := <-updates:
			if update.Message == nil {
				continue
			}
			userID := update.Message.From.ID
			if !b.allowlist[userID] {
				slog.Warn("telegram: unauthorised user", "user_id", userID)
				b.sendText(update.Message.Chat.ID, "Access denied.")
				continue
			}

			actorID := fmt.Sprintf("telegram:%d", userID)
			var reply string
			if b.dispatchFn != nil {
				reply = b.dispatchFn(ctx, actorID, update.Message.Text)
			} else {
				reply = "Dispatcher not configured."
			}
			b.sendText(update.Message.Chat.ID, reply)
		}
	}
}

// Send implements port.Notifier. Notification bodies are authored in markdown
// (the agent layer emits "### headers", "**bold**", "- bullets"), so we convert
// to Telegram HTML before sending — otherwise the raw markup renders as literal
// "###"/"**" noise in the chat. Long messages are split to respect Telegram's
// 4096-char per-message limit.
func (b *Bot) Send(_ context.Context, channel port.NotifyChannel, message string) error {
	chatID, ok := b.channelIDs[string(channel)]
	if !ok {
		slog.Warn("telegram: no chat ID for channel", "channel", channel)
		return nil
	}
	for _, chunk := range splitForTelegram(markdownToHTML(message)) {
		b.sendHTML(chatID, chunk)
	}
	return nil
}

// SendEmbed implements port.Notifier with a MarkdownV2 formatted Telegram message.
func (b *Bot) SendEmbed(_ context.Context, channel port.NotifyChannel, title, body string, fields map[string]string) error {
	chatID, ok := b.channelIDs[string(channel)]
	if !ok {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("*" + escapeMarkdown(title) + "*\n")
	sb.WriteString(escapeMarkdown(body) + "\n")
	for k, v := range fields {
		sb.WriteString("• *" + escapeMarkdown(k) + "*: " + escapeMarkdown(v) + "\n")
	}
	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "MarkdownV2"
	_, err := b.api.Send(msg)
	return err
}

func (b *Bot) sendText(chatID int64, text string) {
	if _, err := b.api.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		slog.Error("telegram: send message failed", "chat_id", chatID, "error", err)
	}
}

// sendHTML sends an HTML-formatted message. If Telegram rejects the markup
// (malformed entity, unsupported tag), it retries once as plain text so the
// operator still receives the content rather than nothing.
func (b *Bot) sendHTML(chatID int64, html string) {
	msg := tgbotapi.NewMessage(chatID, html)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	if _, err := b.api.Send(msg); err != nil {
		slog.Warn("telegram: HTML send failed, retrying as plain text", "chat_id", chatID, "error", err)
		b.sendText(chatID, stripHTMLTags(html))
	}
}

// escapeMarkdown escapes special characters for Telegram MarkdownV2.
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]",
		"(", "\\(", ")", "\\)", "~", "\\~", "`", "\\`",
		">", "\\>", "#", "\\#", "+", "\\+", "-", "\\-",
		"=", "\\=", "|", "\\|", "{", "\\{", "}", "\\}",
		".", "\\.", "!", "\\!",
	)
	return replacer.Replace(s)
}

var (
	mdBold   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdItalic = regexp.MustCompile(`(^|[^*])\*([^*\n]+?)\*`)
	mdCode   = regexp.MustCompile("`([^`\n]+?)`")
	htmlTag  = regexp.MustCompile(`<[^>]+>`)
)

// markdownToHTML converts the limited markdown the agent layer emits into the
// subset of HTML Telegram supports (https://core.telegram.org/bots/api#html-style).
// Telegram HTML only allows <b> <i> <code> <pre> <a> etc., so headers and
// bullets are mapped to bold lines and a "•" prefix rather than real tags.
//
// HTML entities (& < >) in the source text are escaped FIRST so that arbitrary
// LLM output (e.g. "P&L", "a < b") cannot break the message; markup is applied
// afterwards on the already-escaped text.
func markdownToHTML(md string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "### "):
			out = append(out, "<b>"+inlineMD(strings.TrimPrefix(trimmed, "### "))+"</b>")
		case strings.HasPrefix(trimmed, "## "):
			out = append(out, "<b>"+inlineMD(strings.TrimPrefix(trimmed, "## "))+"</b>")
		case strings.HasPrefix(trimmed, "# "):
			out = append(out, "<b>"+inlineMD(strings.TrimPrefix(trimmed, "# "))+"</b>")
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
			out = append(out, "• "+inlineMD(trimmed[2:]))
		default:
			out = append(out, inlineMD(line))
		}
	}
	return strings.Join(out, "\n")
}

// inlineMD escapes HTML entities and applies inline markdown (bold/italic/code)
// for a single line of text.
func inlineMD(s string) string {
	s = escapeHTML(s)
	s = mdBold.ReplaceAllString(s, "<b>$1</b>")
	s = mdItalic.ReplaceAllString(s, "$1<i>$2</i>")
	s = mdCode.ReplaceAllString(s, "<code>$1</code>")
	return s
}

// escapeHTML escapes the three characters Telegram HTML treats as special.
func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// stripHTMLTags removes HTML tags and unescapes entities, producing readable
// plain text for the fallback path when an HTML send is rejected.
func stripHTMLTags(s string) string {
	s = htmlTag.ReplaceAllString(s, "")
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">").Replace(s)
}

// splitForTelegram breaks a message into chunks no larger than Telegram's
// per-message limit, splitting on line boundaries so formatting tags are not
// torn across chunks. A single line longer than the limit is hard-split.
func splitForTelegram(s string) []string {
	if len(s) <= telegramMaxMessageLen {
		return []string{s}
	}

	var chunks []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		// A single oversized line: flush current, then hard-split the line.
		if len(line) > telegramMaxMessageLen {
			if cur.Len() > 0 {
				chunks = append(chunks, cur.String())
				cur.Reset()
			}
			for len(line) > telegramMaxMessageLen {
				chunks = append(chunks, line[:telegramMaxMessageLen])
				line = line[telegramMaxMessageLen:]
			}
			cur.WriteString(line)
			continue
		}
		// +1 accounts for the rejoining "\n".
		if cur.Len()+len(line)+1 > telegramMaxMessageLen {
			chunks = append(chunks, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}
