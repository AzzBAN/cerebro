package cryptopanic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDecryptPosts_GoldenVector pins the whole decryption pipeline against
// a captured real-world response. If CryptoPanic rotates their AES key or
// changes the IV derivation formula, this test fails — which is exactly
// the signal operators need to re-run scripts/extract_cryptopanic_key.sh.
func TestDecryptPosts_GoldenVector(t *testing.T) {
	cipher := mustRead(t, "testdata/posts_ciphertext.b64")
	csrf := strings.TrimSpace(string(mustRead(t, "testdata/posts_csrf.txt")))
	wantRaw := mustRead(t, "testdata/posts_plaintext.json")

	iv := buildIV(ivPrefixPosts, csrf)
	if len(iv) != 16 {
		t.Fatalf("buildIV returned len=%d want=16", len(iv))
	}

	got, err := decryptPosts(iv, strings.TrimSpace(string(cipher)))
	if err != nil {
		t.Fatalf("decryptPosts: %v", err)
	}

	// Compare by re-canonicalising both sides through json.Decode/Encode so
	// that harmless whitespace changes in the fixture don't break the test.
	if !jsonEqual(t, got, wantRaw) {
		t.Fatalf("decrypted payload does not match golden vector\n got=%s\nwant=%s",
			truncate(got, 200), truncate(wantRaw, 200))
	}
}

func TestBuildIV(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		csrf   string
		want   string
	}{
		{
			"posts endpoint — 4-char prefix + 64-char csrf",
			"news",
			"abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ012",
			"newsabcdefghijkl",
		},
		{
			"dashboard endpoint — 'news' + 'rnlist'.repeat(4)",
			"news",
			"rnlistrnlistrnlistrnlist",
			"newsrnlistrnlist",
		},
		{
			"short combined pads with zeros — defensive",
			"a",
			"b",
			"ab\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(buildIV(tt.prefix, tt.csrf))
			if got != tt.want {
				t.Errorf("buildIV(%q, %q) = %q, want %q", tt.prefix, tt.csrf, got, tt.want)
			}
		})
	}
}

func TestDecryptPosts_RejectsBadLen(t *testing.T) {
	tests := []struct {
		name       string
		iv         []byte
		ciphertext string
		wantErrIs  string
	}{
		{"iv too short", []byte("short"), "AAAA", "iv len"},
		{"ciphertext not block multiple", make([]byte, 16), "YWJjZA==", "not a block multiple"},
		{"bad base64", make([]byte, 16), "!!!not-base64", "base64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decryptPosts(tt.iv, tt.ciphertext)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrIs) {
				t.Errorf("err = %v, want substring %q", err, tt.wantErrIs)
			}
		})
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
