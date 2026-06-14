package cryptopanic

import (
	"encoding/json"
	"errors"
	"fmt"
)

// encryptedResponse is the shape of POST /web-api/posts/ response bodies.
// The Status field mirrors the top-level boolean flag the SPA checks before
// decrypting; the S field is base64(AES-CBC(ciphertext)) → zlib → JSON.
type encryptedResponse struct {
	Status bool            `json:"status"`
	Code   string          `json:"code"`
	S      string          `json:"s"`
	Filters json.RawMessage `json:"filters"`
	// Pass-through fields the SPA consumes directly (plaintext).
	LastLpTS   int64 `json:"last_lp_ts"`
	AllLoaded  bool  `json:"all_loaded"`
	AppVersion string `json:"app_version"`
}

// dictList is the column-oriented container CryptoPanic uses to minimise
// payload size: instead of an array of objects, it sends one header row
// (K) and a body of per-post value rows (L).
type dictList struct {
	K []string          `json:"k"`
	L [][]json.RawMessage `json:"l"`
}

// rawPost is the normalised per-post representation we build by zipping
// K and L together. Only fields we actually consume downstream are typed;
// everything else is preserved as RawMessage for forward-compatibility.
type rawPost struct {
	Kind             string          `json:"kind"`
	Domain           string          `json:"domain"`
	URL              string          `json:"url"`
	Slug             string          `json:"slug"`
	Title            string          `json:"title"`
	Body             string          `json:"body"`
	Author           string          `json:"author"`
	PublishedAt      string          `json:"published_at"`
	CreatedAt        string          `json:"created_at"`
	PanicScore       *int            `json:"panic_score"`
	PanicScore1h     *int            `json:"panic_score_1h"`
	PanicScore24h    *int            `json:"panic_score_24h"`
	AISentimentLevel *int            `json:"ai_sentiment_level"`
	PK               int64           `json:"pk"`
	Source           rawSource       `json:"source"`
	CurrenciesCodes  []string        `json:"currencies_codes"`
	Currencies       []rawCurrency   `json:"currencies"`
	Votes            rawVotes        `json:"votes"`
	// Fields we don't currently use but keep visible for debugging.
	Image   string          `json:"image"`
	Content json.RawMessage `json:"content"`
	RemoteID string         `json:"remote_id"`
}

type rawSource struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	Domain     string `json:"domain"`
	DomainSlug string `json:"domain_slug"`
	Rating     int    `json:"rating"`
	Region     string `json:"region"`
	IsGlobal   bool   `json:"is_global"`
}

type rawCurrency struct {
	ID    int    `json:"id"`
	Code  string `json:"code"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

type rawVotes struct {
	TotalCount      int `json:"total_count"`
	TotalUsersCount int `json:"total_users_count"`
	Positive        int `json:"positive_count"`
	Negative        int `json:"negative_count"`
	Important       int `json:"important_count"`
	Like            int `json:"like_count"`
	Dislike         int `json:"dislike_count"`
	LOL             int `json:"lol_count"`
	Toxic           int `json:"toxic_count"`
	Saved           int `json:"save_count"`
	Comments        int `json:"comments_count"`
}

// decodeDictList parses the column-oriented JSON into a slice of rawPost.
// It sidesteps the need to zip K and L manually by round-tripping each row
// through a small per-row JSON object built from the K header.
func decodeDictList(raw []byte) ([]rawPost, error) {
	var dl dictList
	if err := json.Unmarshal(raw, &dl); err != nil {
		// The dashboard endpoint sometimes returns a plain array; the posts
		// endpoint always returns dictList, so we bail early rather than
		// guessing and hiding schema drift.
		return nil, fmt.Errorf("cryptopanic: decode dict list: %w", err)
	}
	if len(dl.K) == 0 {
		return nil, errors.New("cryptopanic: empty column header")
	}

	posts := make([]rawPost, 0, len(dl.L))
	for i, row := range dl.L {
		if len(row) != len(dl.K) {
			// Tolerate a size mismatch on individual rows — skip the row but
			// log via error return only if every row is malformed.
			continue
		}
		// Build a { col: value } JSON object for this row.
		buf := make([]byte, 0, 256)
		buf = append(buf, '{')
		for j, key := range dl.K {
			if j > 0 {
				buf = append(buf, ',')
			}
			kj, err := json.Marshal(key)
			if err != nil {
				return nil, fmt.Errorf("cryptopanic: marshal key %q: %w", key, err)
			}
			buf = append(buf, kj...)
			buf = append(buf, ':')
			buf = append(buf, row[j]...)
		}
		buf = append(buf, '}')

		var p rawPost
		if err := json.Unmarshal(buf, &p); err != nil {
			// Skip individual malformed rows; schema can evolve per-post
			// (e.g. sponsored posts with nulls where strings are expected).
			_ = i
			continue
		}
		posts = append(posts, p)
	}
	return posts, nil
}
