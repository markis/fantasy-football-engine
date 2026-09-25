// Package util holds small shared helpers used across internal packages.
//
// It centralizes a few idioms (string-pointer dereferencing, []any to []string
// coercion) that were previously copy-pasted verbatim into several packages,
// implemented with only the standard library.
package util

import "unicode/utf8"

// NilStr is the stringification of a nil any value: fmt.Sprint(nil) == "<nil>".
// Upstream feeds (Sleeper, FantasyPros) sometimes encode missing fields as this
// literal string, so callers treat it as equivalent to empty.
const NilStr = "<nil>"

// ValueOrEmpty dereferences a pointer, returning "" for nil.
func ValueOrEmpty[T any](v *T) T {
	if v == nil {
		var zero T
		return zero
	}

	return *v
}

// StrOrEmpty dereferences a string pointer, returning "" for nil.
func StrOrEmpty(s *string) string {
	return ValueOrEmpty(s)
}

// StrOr dereferences a string pointer, returning def when it is nil or empty.
func StrOr(s *string, def string) string {
	if v := ValueOrEmpty(s); v != "" {
		return v
	}
	return def
}

// NilIfEmpty returns nil for an empty string, otherwise a pointer to it, so
// nullable schema fields marshal to JSON null instead of "".
func NilIfEmpty(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

// TruncateRunes truncates s to at most limit bytes without splitting a
// multi-byte UTF-8 rune: if the byte cut lands mid-rune, the cut backs up
// to the rune boundary. Postgres rejects text containing partial runes
// (SQLSTATE 22021), so every truncation of text destined for the database
// or the corpus must go through this helper instead of a bare s[:limit].
func TruncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
