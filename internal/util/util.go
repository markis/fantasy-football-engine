// Package util holds small shared helpers used across internal packages.
//
// It centralizes a few idioms (string-pointer dereferencing, []any to []string
// coercion) that were previously copy-pasted verbatim into several packages,
// and exposes thin wrappers over github.com/samber/lo for the collection
// operations that recur in the codebase.
package util

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/samber/lo"
)

// NilStr is the stringification of a nil any value: fmt.Sprint(nil) == "<nil>".
// Upstream feeds (Sleeper, FantasyPros) sometimes encode missing fields as this
// literal string, so callers treat it as equivalent to empty.
const NilStr = "<nil>"

// StrOrEmpty dereferences a string pointer, returning "" for nil.
func StrOrEmpty(s *string) string {
	return lo.FromPtr(s)
}

// StrOr dereferences a string pointer, returning def when it is nil or empty.
func StrOr(s *string, def string) string {
	if v := lo.FromPtr(s); v != "" {
		return v
	}
	return def
}

// NilIfEmpty returns nil for an empty string, otherwise a pointer to it, so
// nullable schema fields marshal to JSON null instead of "".
func NilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ToStringSlice coerces a []any (as produced by encoding/json) into []string,
// dropping empty and "<nil>" entries. It returns nil when v is not a []any.
func ToStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	return lo.Filter(
		lo.Map(arr, func(item any, _ int) string { return fmt.Sprint(item) }),
		func(s string, _ int) bool { return s != "" && s != NilStr },
	)
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

// AsMap returns v as a map[string]any, or nil if v is nil or not such a map.
// Used to pass through opaque nested JSON objects (settings, metadata).
func AsMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

// ToIntSlice coerces a decoded-JSON array of numbers (or numeric strings) to
// []int, returning an empty slice for nil/non-array. Mirrors the prior
// sync.toIntSlice coercion.
func ToIntSlice(v any) []int {
	if v == nil {
		return []int{}
	}
	arr, ok := v.([]any)
	if !ok {
		return []int{}
	}
	result := make([]int, 0, len(arr))
	for _, item := range arr {
		result = append(result, ToInt(item))
	}
	return result
}

// EpochMsToTimePtr converts a decoded-JSON epoch-millis value (number or
// numeric string) to a *time.Time in UTC, returning nil for null/unparseable
// so it persists as SQL NULL. Mirrors the prior sync.epochMsToTime semantics.
func EpochMsToTimePtr(v any) *time.Time {
	var ms int64
	switch x := v.(type) {
	case nil:
		return nil
	case float64:
		ms = int64(x)
	case int:
		ms = int64(x)
	case int64:
		ms = x
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return nil
		}
		ms = n
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return nil
		}
		ms = int64(n)
	default:
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}
