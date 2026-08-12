package sleeper

import (
	"strconv"

	"ff-engine/internal/util"
)

// ptr-coercion helpers for nullable scalar fields decoded from raw
// map[string]any payloads. Each returns nil for a JSON null so the field
// marshals back to null (and persists as SQL NULL), and a pointer to the
// coerced value otherwise. They exist because Sleeper sends these scalars
// as JSON numbers that encoding/json decodes into float64.

// boolPtr returns *bool for v: nil for null, otherwise the bool value (the
// only shape Sleeper sends for boolean flags). Non-bool, non-nil values
// yield nil rather than a guess.
func boolPtr(v any) *bool {
	if v == nil {
		return nil
	}
	if b, ok := v.(bool); ok {
		return &b
	}
	return nil
}

// intPtr returns *int for v: nil for null, otherwise util.ToInt(v). Used for
// nullable integer fields like matchup_id.
func intPtr(v any) *int {
	if v == nil {
		return nil
	}
	n := util.ToInt(v)
	return &n
}

// floatPtr returns *float64 for v: nil for null, otherwise the coerced
// float. Used for nullable score fields (points, custom_points).
func floatPtr(v any) *float64 {
	if v == nil {
		return nil
	}
	f := toFloat(v)
	return &f
}

// toFloat coerces a decoded-JSON scalar (float64, int, or numeric string) to
// float64, returning 0 for absent/unparseable values. Mirrors util.ToInt's
// coercion philosophy for Sleeper's occasionally-stringy numeric fields.
func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0
		}
		return f
	}
	return 0
}
