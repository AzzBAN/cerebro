#!/usr/bin/env bash
#
# extract_cryptopanic_key.sh
#
# Refresh the AES key embedded in internal/ingest/news/cryptopanic/key.go
# when the live CryptoPanic JS bundle rotates.
#
# Usage: scripts/extract_cryptopanic_key.sh
#
# Requires: curl, node, python3 (stdlib only)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KEY_FILE="$ROOT/internal/ingest/news/cryptopanic/key.go"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "==> Fetching cryptopanic.com homepage..."
curl -sSf -A "Mozilla/5.0" "https://cryptopanic.com/" > "$TMP/home.html"

BUNDLE_URL="$(grep -oE 'src="[^"]*cryptopanic\.min\.[a-f0-9]+\.js"' "$TMP/home.html" | head -n1 | sed -E 's/^src="([^"]+)".*$/\1/')"
if [[ -z "$BUNDLE_URL" ]]; then
  echo "!! could not locate cryptopanic.min.*.js in homepage HTML" >&2
  exit 1
fi

BUNDLE_HASH="$(echo "$BUNDLE_URL" | sed -E 's#.*cryptopanic\.min\.([a-f0-9]+)\.js#\1#')"
echo "    bundle: $BUNDLE_URL"
echo "    hash:   $BUNDLE_HASH"

echo "==> Downloading JS bundle..."
curl -sSf -A "Mozilla/5.0" "$BUNDLE_URL" > "$TMP/bundle.js"

echo "==> Extracting packed dk() function..."
BUNDLE="$TMP/bundle.js" python3 - <<'PY' > "$TMP/dk.js"
import re, sys, os
src = open(os.environ['BUNDLE']).read()
m = re.search(r'function dk\(\)\s*\{', src)
if not m:
    sys.stderr.write("!! could not find `function dk(){` in bundle\n")
    sys.exit(2)
i = m.end()
depth = 1
while i < len(src) and depth > 0:
    c = src[i]
    if c == '"' or c == "'":
        q = c
        i += 1
        while i < len(src) and src[i] != q:
            i += 2 if src[i] == '\\' else 1
        i += 1
        continue
    if c == '{': depth += 1
    elif c == '}': depth -= 1
    i += 1
print(src[m.start():i])
PY

echo "==> Executing dk() in Node to resolve the key..."
KEY="$(node -e "$(cat "$TMP/dk.js"); const k = dk(); console.log(k);")"
KEY_LEN="${#KEY}"
if [[ "$KEY_LEN" -ne 16 ]]; then
  echo "!! extracted key length is $KEY_LEN, expected 16" >&2
  echo "   key=<$KEY>" >&2
  exit 3
fi
echo "    key:  <$KEY>  (len=$KEY_LEN)"

echo "==> Current key.go:"
grep 'const aesKey' "$KEY_FILE" || true
grep 'sourceBundleHash' "$KEY_FILE" || true

echo
echo "==> Suggested replacement in $KEY_FILE:"
cat <<EOF

const aesKey = \`$KEY\`
const sourceBundleHash = "$BUNDLE_HASH"
EOF

echo
echo "If this differs from the current file, update key.go and re-run:"
echo "    go test ./internal/ingest/news/cryptopanic"
echo
echo "The golden-vector test will fail if you also need a fresh testdata/"
echo "ciphertext capture (regenerate with a one-shot Node script)."
