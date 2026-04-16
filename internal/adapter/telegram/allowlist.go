package telegram

import (
	"strconv"

	"github.com/azhar/cerebro/internal/config"
)

// ParseAllowlist converts config string IDs to int64 Telegram user IDs.
func ParseAllowlist(cfg config.SecretsConfig) []int64 {
	var ids []int64
	for _, s := range cfg.TelegramAllowlistUserIDs {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
