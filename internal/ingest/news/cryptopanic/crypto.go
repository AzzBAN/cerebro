package cryptopanic

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// ErrBadPayload indicates the encrypted blob could not be decrypted or
// decompressed — usually meaning CryptoPanic rotated their AES key or
// changed the IV derivation. Callers should treat this as a signal to
// fall back to the Chromium scraper and alert operators.
var ErrBadPayload = errors.New("cryptopanic: decrypt/inflate failed (likely key rotation)")

// decryptPosts decrypts the base64-encoded `s` field of a /web-api/posts/
// response and returns the raw JSON plaintext.
//
// The IV is derived by the caller and passed here as a raw 16-byte slice;
// buildIV is the helper that mirrors the JavaScript formula exactly.
func decryptPosts(ivBytes []byte, ciphertextB64 string) ([]byte, error) {
	if len(ivBytes) != aes.BlockSize {
		return nil, fmt.Errorf("%w: iv len=%d want=%d", ErrBadPayload, len(ivBytes), aes.BlockSize)
	}
	ct, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("%w: base64: %v", ErrBadPayload, err)
	}
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: ciphertext len=%d not a block multiple", ErrBadPayload, len(ct))
	}

	block, err := aes.NewCipher([]byte(aesKey))
	if err != nil {
		// Compile-time invariant; aesKey is 16 bytes.
		return nil, fmt.Errorf("%w: cipher: %v", ErrBadPayload, err)
	}

	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, ivBytes).CryptBlocks(pt, ct)

	// Zero-padding: strip trailing 0x00 bytes. This is safe because the
	// following zlib layer starts with a fixed 0x78 header, so a truncation
	// into valid data would be detected downstream.
	pt = bytes.TrimRight(pt, "\x00")

	zr, err := zlib.NewReader(bytes.NewReader(pt))
	if err != nil {
		return nil, fmt.Errorf("%w: zlib header: %v", ErrBadPayload, err)
	}
	defer zr.Close()

	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("%w: inflate: %v", ErrBadPayload, err)
	}
	return out, nil
}

// buildIV mirrors the JavaScript dcList formula:
//
//	iv = (prefix + csrftoken).substr(0, 16)
//
// Because CryptoPanic calls dcList("news", response.s) for the posts
// endpoint and the csrftoken is always 64 ASCII chars, the result is
// simply prefix + csrftoken[: 16-len(prefix) ].
func buildIV(prefix, csrftoken string) []byte {
	combined := prefix + csrftoken
	if len(combined) < aes.BlockSize {
		// Pad with zeros to the left; this branch is defensive — the
		// website always produces a 68-char combined string for posts.
		padded := make([]byte, aes.BlockSize)
		copy(padded, combined)
		return padded
	}
	return []byte(combined[:aes.BlockSize])
}
