package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

// News tool output caps. Every item the LLM sees costs tokens on every
// subsequent turn (full history is re-sent), so the defaults here are
// tight. Operators asking the agent for more detail can override via the
// limit argument up to maxNewsLimit.
const (
	defaultNewsLimit  = 5
	maxNewsLimit      = 10
	maxTitleChars     = 140
	maxSummaryChars   = 200
	maxCurrenciesPerItem = 4
)

// newsItemProjected is the minimal shape of a NewsItem we send to the LLM.
// Dropping Source/Domain/Votes/Kind/ID shaves ~40-80 tokens per item.
type newsItemProjected struct {
	Title       string    `json:"title"`
	Summary     string    `json:"summary,omitempty"`
	URL         string    `json:"url,omitempty"`
	Currencies  []string  `json:"currencies,omitempty"`
	Sentiment   string    `json:"sentiment,omitempty"`
	PublishedAt time.Time `json:"published_at"`
}

// projectNewsItems truncates and projects raw NewsItems into the minimal
// shape above. Keep this in sync with newsItemProjected.
func projectNewsItems(items []port.NewsItem) []newsItemProjected {
	out := make([]newsItemProjected, 0, len(items))
	for _, it := range items {
		currencies := it.Currencies
		if len(currencies) > maxCurrenciesPerItem {
			currencies = currencies[:maxCurrenciesPerItem]
		}
		out = append(out, newsItemProjected{
			Title:       truncate(it.Title, maxTitleChars),
			Summary:     truncate(it.Summary, maxSummaryChars),
			URL:         it.URL,
			Currencies:  currencies,
			Sentiment:   it.Sentiment,
			PublishedAt: it.PublishedAt,
		})
	}
	return out
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	// Avoid slicing mid-rune: back up to a UTF-8 boundary.
	cutoff := n
	for cutoff > 0 && (s[cutoff]&0xC0) == 0x80 {
		cutoff--
	}
	return s[:cutoff] + "…"
}

// FetchLatestNews implements the fetch_latest_news agent tool.
//
// The tool first tries the Redis cache populated by the combined news
// ingest runner (news:latest holds the merged CryptoPanic + FinancialJuice
// stream; news:by_asset:<CODE> is CryptoPanic-only since FJ has no
// asset-keyed query). It only falls back to a live FetchLatest call on
// cache miss. This matters during ReAct loops where the agent may call
// the tool multiple times per run — we do not want to spawn a Chromium
// instance or bounce through the CSRF dance on every call.
func FetchLatestNews(feed port.NewsFeed, cache port.Cache) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("fetch_latest_news: bad args: %w", err)
			}
			// Clamp limit. The LLM frequently asks for 10-20 items "just in
			// case"; each item is ~150-250 tokens so this explodes the
			// context budget fast. 5 is enough for sentiment + headline
			// scanning and keeps a single call under ~1k tokens.
			if args.Limit <= 0 {
				args.Limit = defaultNewsLimit
			}
			if args.Limit > maxNewsLimit {
				args.Limit = maxNewsLimit
			}

			items := readNewsFromCache(ctx, cache, args.Query, args.Limit)
			if len(items) == 0 && feed != nil {
				// Only pass the query to the live feed as an asset filter
				// when it looks like a valid ticker. Natural-language
				// queries ("bitcoin reserve us government") get sent as
				// an empty asset so the feed returns the global stream;
				// we'll then filter by keywords via the cache path on
				// the next cycle.
				assetArg := ""
				if looksLikeTicker(args.Query) {
					assetArg = args.Query
				}
				live, err := feed.FetchLatest(ctx, assetArg, args.Limit)
				if err != nil {
					return nil, fmt.Errorf("fetch_latest_news: %w", err)
				}
				items = live
			}

			if len(items) == 0 {
				return json.Marshal(map[string]any{
					"items":   []port.NewsItem{},
					"message": "No news found for the given query. Try broader keywords like 'crypto', 'BTC', or 'market'.",
				})
			}
			// Project to a minimal wire format: drop long body text that
			// the LLM almost never cites, and truncate titles to cut
			// context bloat.
			return json.Marshal(map[string]any{"items": projectNewsItems(items)})
		},
		Definition: port.ToolDefinition{
			Name:        "fetch_latest_news",
			Description: "Search for recent news headlines. Pass any relevant keywords as the query — asset names (BTC, ETH, XAU, GOLD), market topics (rates, inflation, ETF), or events (halving, FOMC, regulation). Try multiple searches with different keywords for comprehensive coverage.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search keywords: asset tickers (BTC, ETH, XAU), topics (inflation, ETF, regulation), or events (FOMC, halving). Use specific and broad queries for best results.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max number of items to return (default 5, hard cap 10)",
					},
				},
			},
		},
	}
}

// readNewsFromCache looks up the CryptoPanic ingest cache for either a
// ticker-specific list (news:by_asset:BTC) or the global list
// (news:latest). Returns nil on any error — the caller falls back to a
// live fetch. query is treated as a ticker match when it looks like one
// (all-caps, <=10 chars, alphanumeric) and as a keyword filter against
// titles/currencies otherwise.
func readNewsFromCache(ctx context.Context, cache port.Cache, query string, limit int) []port.NewsItem {
	if cache == nil {
		return nil
	}

	query = strings.TrimSpace(query)
	var candidate string
	if looksLikeTicker(query) {
		candidate = "news:by_asset:" + strings.ToUpper(query)
	} else {
		candidate = "news:latest"
	}

	raw, err := cache.Get(ctx, candidate)
	if err != nil || len(raw) == 0 {
		// On miss for a specific asset, fall back to the global cache so
		// we still return *something* related rather than forcing a live
		// scrape for every exotic ticker the agent asks about.
		if candidate != "news:latest" {
			raw, err = cache.Get(ctx, "news:latest")
		}
		if err != nil || len(raw) == 0 {
			return nil
		}
	}

	var items []port.NewsItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}

	// Keyword filter: non-ticker queries narrow the global list by matching
	// against title, currencies, and domain.
	if query != "" && !looksLikeTicker(query) {
		q := strings.ToLower(query)
		filtered := items[:0]
		for _, it := range items {
			if matchesKeyword(it, q) {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func looksLikeTicker(q string) bool {
	if q == "" || len(q) > 10 {
		return false
	}
	for _, r := range q {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func matchesKeyword(it port.NewsItem, q string) bool {
	if strings.Contains(strings.ToLower(it.Title), q) {
		return true
	}
	if strings.Contains(strings.ToLower(it.Domain), q) {
		return true
	}
	for _, c := range it.Currencies {
		if strings.Contains(strings.ToLower(c), q) {
			return true
		}
	}
	return false
}
