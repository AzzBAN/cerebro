package port

import "context"

// NotifyChannel identifies a ChatOps destination channel.
type NotifyChannel string

const (
	ChannelTradeExecution   NotifyChannel = "trade_execution"
	ChannelAIReasoning      NotifyChannel = "ai_reasoning"
	ChannelSystemAlerts     NotifyChannel = "system_alerts"
	ChannelDefault          NotifyChannel = "default"
)

// Notifier sends operator alerts and trade notifications.
// Implemented by the Telegram and Discord adapters.
type Notifier interface {
	// Send delivers a plain-text message to the given channel.
	Send(ctx context.Context, channel NotifyChannel, message string) error

	// SendEmbed delivers a rich embed (Discord) or formatted message (Telegram).
	SendEmbed(ctx context.Context, channel NotifyChannel, title, body string, fields map[string]string) error
}
