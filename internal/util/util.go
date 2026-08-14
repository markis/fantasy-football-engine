// Package util holds small shared helpers used across internal packages.
//
// It centralizes a few idioms (string-pointer dereferencing, []any to []string
// coercion) that were previously copy-pasted verbatim into several packages,
// implemented with only the standard library.
package util

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
