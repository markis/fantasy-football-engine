package pipeline

import "testing"

func TestIsNonFootballSport(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		content string
		want    bool
	}{
		{"baseball in title", "MLB Trade Rumors", "", true},
		{"basketball in content", "Fantasy News", "NBA scores from last night", true},
		{"hockey word match", "NHL Fantasy", "hockey stats update", true},
		{"soccer in content", "Premier League", "soccer transfer news", true},
		{"football only", "Patrick Mahomes Injury Update", "Chiefs QB expected to play Sunday", false},
		{"MLB substring not whole word", "Alumni reunion event", "", false},
		{"empty", "", "", false},
		{"word boundary: m lbs", "Weight room", "he lost 5 lbs", false},
		{"NBA in title", "NBA Finals Preview", "", true},
		{"mixed case MLB", "mlb power rankings", "", true},
		{"NBAR as not match", "Update on Anbar situation", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNonFootballSport(tt.title, tt.content); got != tt.want {
				t.Errorf("isNonFootballSport(%q, %q) = %v, want %v", tt.title, tt.content, got, tt.want)
			}
		})
	}
}
