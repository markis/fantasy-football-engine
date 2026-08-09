package pipeline

import (
	"crypto/md5"
	"encoding/binary"
	"math"
	"regexp"
	"strings"
)

// HashBits is the SimHash fingerprint width.
const HashBits = 64

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)
var whitespaceRe = regexp.MustCompile(`\s+`)

// SimhashCompute computes a 64-bit SimHash fingerprint for the given text.
// The result is masked to signed 64-bit range for PostgreSQL bigint compatibility.
func SimhashCompute(text string) int64 {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return 0
	}

	v := make([]int, HashBits)
	for _, token := range tokens {
		h := md5.Sum([]byte(token))
		hash := int64(binary.BigEndian.Uint64(h[:8]))
		for i := 0; i < HashBits; i++ {
			if hash&(1<<int64(i)) != 0 {
				v[i]++
			} else {
				v[i]--
			}
		}
	}

	var fingerprint int64
	for i := 0; i < HashBits; i++ {
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
	x := uint64(a) ^ uint64(b)
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

// Ensure math import is used (for potential future use)
var _ = math.MaxFloat64