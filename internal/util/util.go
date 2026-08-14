// Package util holds small shared helpers used across internal packages.
//
// It centralizes a few idioms (string-pointer dereferencing, []any to []string
// coercion) that were previously copy-pasted verbatim into several packages,
// implemented with only the standard library.
package util

import (
	"encoding/json"
	"strconv"
)

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

// ToInt coerces a decoded-JSON value (float64, int, json.Number, or numeric
// string) to an int, returning 0 if it cannot be interpreted as a number.
// Mirrors the prior corpus.anyToInt / sync.toInt coercion so call sites can be
// migrated without behavior change.
func ToInt(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case int:
		return t
	case float64:
		return int(t)
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	case string:
		n, err := strconv.Atoi(t)
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}
