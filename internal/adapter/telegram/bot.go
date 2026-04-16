package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/azhar/cerebro/internal/port"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

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

// Send implements port.Notifier by sending a text message to the channel's chat ID.
func (b *Bot) Send(_ context.Context, channel port.NotifyChannel, message string) error {
	chatID, ok := b.channelIDs[string(channel)]
	if !ok {
		slog.Warn("telegram: no chat ID for channel", "channel", channel)
		return nil
	}
	b.sendText(chatID, message)
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
