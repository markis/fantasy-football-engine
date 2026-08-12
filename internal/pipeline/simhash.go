package pipeline

import (
	"crypto/md5" // #nosec G501 -- MD5 used for locality-sensitive hashing, not cryptographic security
	"encoding/binary"
	"regexp"
	"strings"
)

// HashBits is the SimHash fingerprint width.
const HashBits = 64

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
	whitespaceRe = regexp.MustCompile(`\s+`)
)

// SimhashCompute computes a 64-bit SimHash fingerprint for the given text.
// The result is masked to signed 64-bit range for PostgreSQL bigint compatibility.
func SimhashCompute(text string) int64 {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return 0
	}

	v := make([]int, HashBits)
	for _, token := range tokens {
		h := md5.Sum([]byte(token))                   // #nosec G401 -- MD5 used for locality-sensitive hashing, not cryptographic security
		hash := int64(binary.BigEndian.Uint64(h[:8])) // #nosec G115 -- 8-byte MD5 prefix cast; overflow acceptable for fingerprinting
		for i := range HashBits {
			if hash&(1<<int64(i)) != 0 {
				v[i]++
			} else {
				v[i]--
			}
		}
	}

	var fingerprint int64
	for i := range HashBits {
		if v[i] > 0 {
			fingerprint |= 1 << int64(i)
		}
	}
	// Mask to signed 64-bit range for PostgreSQL bigint compatibility
	u := uint64(fingerprint)
	if u >= (uint64(1) << 63) {
		u = u - (uint64(1) << 63) - (uint64(1) << 63) // subtract 2^64
		fingerprint = int64(u)
	}
	return fingerprint
}

// HammingDistance returns the Hamming distance between two int64 fingerprints.
func HammingDistance(a, b int64) int {
	x := uint64(a) ^ uint64(b) // #nosec G115 -- signed->unsigned cast of a hash value; safe for fingerprinting
	count := 0
	for x > 0 {
		count++
		x &= x - 1
	}
	return count
}

func tokenize(text string) []string {
	text = htmlTagRe.ReplaceAllString(text, " ")
	text = whitespaceRe.ReplaceAllString(text, " ")
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return nil
	}
	return strings.Split(text, " ")
}
