package util

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{name: "under limit", in: "hello", limit: 10, want: "hello"},
		{name: "ascii cut", in: "hello world", limit: 5, want: "hello"},
		{name: "cut on rune boundary", in: "ab\u2014cd", limit: 3, want: "ab"},
		{name: "cut mid rune backs up", in: "ab\u2014cd", limit: 4, want: "ab"},
		{name: "cut lands after rune", in: "ab\u2014cd", limit: 5, want: "ab\u2014"},
		{name: "zero limit", in: "hello", limit: 0, want: ""},
		{name: "negative limit", in: "hello", limit: -1, want: ""},
		{name: "empty input", in: "", limit: 5, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateRunes(tt.in, tt.limit)
			if got != tt.want {
				t.Errorf("TruncateRunes(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result %q is not valid UTF-8", got)
			}
		})
	}
}
