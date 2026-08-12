package sleeper

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"ff-engine/internal/util"
)

// This file defines the lenient scalar/slice types used by the sleeper API
// structs so that encoding/json can decode real Sleeper payloads directly,
// without a manual map[string]any -> Parse* step.
//
// Sleeper is inconsistent: the "same" field arrives as a JSON string in some
// records and a number in others (owner_id, external IDs), some integers
// arrive as numeric strings (traded-pick season "2019", draft-pick roster_id
// "1"), and nullable scalars must keep null distinct from a zero value (so
// they persist as SQL NULL and marshal back to JSON null). These types encode
// that coercion once, in their UnmarshalJSON, and compose via plain pointers:
// a *T field decodes JSON null to a nil pointer (encoding/json sets the
// pointer to nil without calling UnmarshalJSON), while a value T field calls
// UnmarshalJSON with the literal "null" and coerces to the zero value.

// errFlexStringDecode is returned by FlexString.UnmarshalJSON when the JSON
// value is neither a string, a number, nor null.
var errFlexStringDecode = errors.New("FlexString: cannot decode JSON value")

// jsonNull is the literal null token, factored out so goconst stays quiet.
const jsonNull = "null"

// FlexString is a string that unmarshals from a JSON string OR number,
// coercing the number to its decimal string form. A null produces "" for a
// value FlexString and a nil pointer for a *FlexString (encoding/json sets
// the pointer to nil without calling UnmarshalJSON). It exists because Sleeper
// encodes several ID fields as numbers in some records and strings in others
// (e.g. rotoworld_id arrives as 8356, owner_id as "188815879448829952").
type FlexString string

// UnmarshalJSON accepts a JSON string, number, or null, coercing to
// FlexString (null and "" both yield "").
func (f *FlexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = FlexString(string(n))
		return nil
	}
	return fmt.Errorf("%w: %s", errFlexStringDecode, b)
}

// Ptr returns a *string copy of f, or nil if f is nil. Used to feed nullable
// TEXT columns when persisting.
func (f *FlexString) Ptr() *string {
	if f == nil {
		return nil
	}
	s := string(*f)
	return &s
}

// FlexInt is an int that unmarshals from a JSON number or numeric string,
// coercing either form to int. null, absent, and any unparseable value yield
// 0 — mirroring util.ToInt so downstream int usage never panics.
type FlexInt int

// UnmarshalJSON accepts a JSON number, numeric string, or null.
func (f *FlexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == jsonNull {
		*f = 0
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		if i, perr := n.Int64(); perr == nil {
			*f = FlexInt(i)
			return nil
		}
		if fl, ferr := n.Float64(); ferr == nil {
			*f = FlexInt(int(fl))
			return nil
		}
		*f = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if i, perr := strconv.Atoi(s); perr == nil {
			*f = FlexInt(i)
			return nil
		}
		if fl, ferr := strconv.ParseFloat(s, 64); ferr == nil {
			*f = FlexInt(int(fl))
			return nil
		}
	}
	*f = 0
	return nil
}

// FlexFloat is a float64 that unmarshals from a JSON number or numeric string.
// null/absent/unparseable yield 0 — mirroring the toFloat coercion used for
// nullable score fields.
type FlexFloat float64

// UnmarshalJSON accepts a JSON number, numeric string, or null.
func (f *FlexFloat) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == jsonNull {
		*f = 0
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		if fl, perr := n.Float64(); perr == nil {
			*f = FlexFloat(fl)
			return nil
		}
		*f = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if fl, err := strconv.ParseFloat(s, 64); err == nil {
			*f = FlexFloat(fl)
			return nil
		}
	}
	*f = 0
	return nil
}

// FlexBool is a bool that unmarshals from a JSON bool. null yields false for a
// value FlexBool and a nil pointer for a *FlexBool (the form used for
// is_owner/is_keeper, so a JSON null stays nil and persists as SQL NULL).
// Non-bool, non-null values yield false; Sleeper only sends bools for these
// fields, so that path is defensive.
type FlexBool bool

// UnmarshalJSON accepts a JSON bool or null.
func (f *FlexBool) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == jsonNull {
		*f = false
		return nil
	}
	var bl bool
	if err := json.Unmarshal(b, &bl); err == nil {
		*f = FlexBool(bl)
		return nil
	}
	*f = false
	return nil
}

// FlexStringSlice is a []string that unmarshals from a JSON array of strings
// and/or numbers, dropping empty and "<nil>" entries. null and non-arrays
// yield nil — mirroring util.ToStringSlice.
type FlexStringSlice []string

// UnmarshalJSON accepts a JSON array (of strings/numbers), null, or non-array.
func (s *FlexStringSlice) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == jsonNull {
		*s = nil
		return nil
	}
	var raw []json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		*s = nil
		return nil //nolint:nilerr // non-array payloads coerce to nil, matching util.ToStringSlice
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		var fs FlexString
		if json.Unmarshal(item, &fs) != nil {
			continue
		}
		v := string(fs)
		if v == "" || v == util.NilStr {
			continue
		}
		out = append(out, v)
	}
	*s = out
	return nil
}

// FlexIntSlice is a []int that unmarshals from a JSON array of numbers and/or
// numeric strings, coercing each element to int (0 for unparseable). null and
// non-arrays yield an empty (non-nil) slice — mirroring util.ToIntSlice.
type FlexIntSlice []int

// UnmarshalJSON accepts a JSON array (of numbers/numeric strings), null, or
// non-array.
func (s *FlexIntSlice) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == jsonNull {
		*s = []int{}
		return nil
	}
	var raw []json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		*s = []int{}
		return nil //nolint:nilerr // non-array payloads coerce to an empty slice, matching util.ToIntSlice
	}
	out := make([]int, 0, len(raw))
	for _, item := range raw {
		var fi FlexInt
		if json.Unmarshal(item, &fi) != nil {
			continue
		}
		out = append(out, int(fi))
	}
	*s = out
	return nil
}

// EpochMs is a time.Time that unmarshals from a JSON epoch-millis number or
// numeric string. It marshals back as RFC3339 (not the raw epoch number), so
// archived jsonb values stay human-readable. Used as *EpochMs for nullable
// timestamp fields: a JSON null yields a nil pointer (encoding/json sets the
// pointer to nil without calling UnmarshalJSON), which persists as SQL NULL.
type EpochMs time.Time

// UnmarshalJSON accepts a JSON epoch-millis number, numeric string, or null.
func (e *EpochMs) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == jsonNull {
		*e = EpochMs{}
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		if ms, perr := n.Int64(); perr == nil {
			*e = EpochMs(time.UnixMilli(ms).UTC())
			return nil
		}
		if fl, ferr := n.Float64(); ferr == nil {
			*e = EpochMs(time.UnixMilli(int64(fl)).UTC())
			return nil
		}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if ms, perr := strconv.ParseInt(s, 10, 64); perr == nil {
			*e = EpochMs(time.UnixMilli(ms).UTC())
			return nil
		}
		// Also accept RFC3339 so the value survives a marshal/unmarshal
		// round-trip (the client cache re-marshals decoded structs, and
		// MarshalJSON emits RFC3339, not the original epoch-ms number).
		if t, terr := time.Parse(time.RFC3339, s); terr == nil {
			*e = EpochMs(t.UTC())
			return nil
		}
	}
	return nil
}

// MarshalJSON serializes the timestamp as RFC3339, or null for the zero value.
func (e EpochMs) MarshalJSON() ([]byte, error) {
	t := time.Time(e)
	if t.IsZero() {
		return []byte(jsonNull), nil
	}
	b, err := t.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal epoch time: %w", err)
	}
	return b, nil
}
