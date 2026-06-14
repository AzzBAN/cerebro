package cryptopanic

// aesKey is the AES-128 key used by CryptoPanic's Vue app to encrypt the
// /web-api/posts/ response body. It is embedded in the minified JS bundle
// inside a Dean Edwards Packer call:
//
//	dk = function() { return eval(function(...){...}("')0*$+)/1}$2>/3';",0,4,"b7Z|T|9|L".split("|"),0,{})) }
//
// Unpacking yields the 16-byte string below. When CryptoPanic rebuilds
// their frontend bundle this value may rotate; see scripts/extract_cryptopanic_key.sh
// for the refresh procedure.
const aesKey = `)b7Z*$+)/T}$9>/L`

// sourceBundleHash pins the JS bundle hash from which aesKey was extracted.
// When the RE path starts failing, compare against the current bundle URL
// reported by https://cryptopanic.com/ to confirm a key rotation happened.
const sourceBundleHash = "ee3f6895b481"

// ivPrefixPosts is the caller-supplied IV prefix used at the posts callsite:
//
//	s = (0, g.dcList)("news", i.data.s)
//
// dcList derives the IV as (prefix + csrftoken).substr(0, 16).
const ivPrefixPosts = "news"
