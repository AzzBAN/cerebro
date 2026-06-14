//go:build live
// +build live

package cryptopanic

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestLive_FetchPosts hits the real CryptoPanic site. Gated behind the
// "live" build tag to keep it out of CI; run manually with:
//
//	go test -tags=live ./internal/ingest/news/cryptopanic -run Live -v
func TestLive_FetchPosts(t *testing.T) {
	c, err := NewClient(20 * time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	posts, err := c.FetchPosts(context.Background(), Query{Filter: "hot", Kind: "news"})
	if err != nil {
		t.Fatalf("FetchPosts: %v", err)
	}
	if len(posts) == 0 {
		t.Fatal("expected at least one post")
	}
	t.Logf("fetched %d posts, first: pk=%d title=%q domain=%s",
		len(posts), posts[0].PK, truncStr(posts[0].Title, 60), posts[0].Source.Domain)
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ = fmt.Sprintf
