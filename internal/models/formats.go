package models

const (
	tepPPPlus = "te++"
	typeType  = "dynasty"
	typeNone  = "none"
)

// LeagueFormat describes a Sleeper league's scoring format and its
// FantasyCalc market mapping.
type LeagueFormat struct {
	Name   string `json:"name"`
	Teams  int    `json:"teams"`
	NumQbs int    `json:"numQbs"`
	PPR    int    `json:"ppr"`
	TEP    string `json:"tep"`
	Market *int   `json:"market"`
	Type   string `json:"type"`
}

// LeagueFormats is the authoritative mapping of Markis's Sleeper league IDs
// to their scoring formats and FantasyCalc market IDs.
var LeagueFormats = map[string]LeagueFormat{
	"1343410990741479425": {Name: "Poor Life Choices", Teams: 18, NumQbs: 2, PPR: 1, TEP: tepPPPlus, Market: nil, Type: "redraft"},
	"1312178509908545536": {Name: "Backyard Brawl", Teams: 12, NumQbs: 1, PPR: 1, TEP: typeNone, Market: new(1), Type: typeType},
	"1312183009700499456": {Name: "Mama Says Foosball", Teams: 12, NumQbs: 2, PPR: 1, TEP: tepPPPlus, Market: new(2), Type: typeType},
	"1312170789964898304": {Name: "Deja Vu Dynasty", Teams: 12, NumQbs: 2, PPR: 1, TEP: tepPPPlus, Market: new(2), Type: typeType},
	"1312051514470055936": {Name: "Dave is the best", Teams: 10, NumQbs: 2, PPR: 0, TEP: typeNone, Market: new(3), Type: typeType},
}

// FormatCombo is a distinct dynasty format combo synced from FantasyCalc.
type FormatCombo struct {
	Market int    `json:"market"`
	Teams  int    `json:"teams"`
	NumQbs int    `json:"numQbs"`
	PPR    int    `json:"ppr"`
	TEP    string `json:"tep"`
	Label  string `json:"label"`
}

// FormatCombos are the distinct dynasty format combos to sync.
var FormatCombos = []FormatCombo{
	{Market: 1, Teams: 12, NumQbs: 1, PPR: 1, TEP: typeNone, Label: "12t-1QB-PPR-none"},
	{Market: 2, Teams: 12, NumQbs: 2, PPR: 1, TEP: tepPPPlus, Label: "12t-SF-PPR-TEP1.0"},
	{Market: 3, Teams: 10, NumQbs: 2, PPR: 0, TEP: typeNone, Label: "10t-SF-HalfPPR-none"},
}

// MarketLabel maps a FantasyCalc market ID to its label.
var MarketLabel = map[int]string{
	1: "12t-1QB-PPR-none",
	2: "12t-SF-PPR-TEP1.0",
	3: "10t-SF-HalfPPR-none",
}

// MarkisUserID is Markis's Sleeper user ID.
const MarkisUserID = "558115100726579200"

// PickValue constants — 1QB dynasty pick values.
var (
	Round1       = map[int]int{1: 9000, 2: 8200, 3: 7600, 4: 6800, 5: 6200, 6: 5600, 7: 5000, 8: 4500, 9: 4000, 10: 3600, 11: 3200, 12: 2900}
	Round2       = map[int]int{1: 2600, 2: 2300, 3: 2100, 4: 1900, 5: 1700, 6: 1500, 7: 1400, 8: 1300, 9: 1200, 10: 1100, 11: 1000, 12: 950}
	RoundDefault = map[int]int{1: 3400, 2: 2000, 3: 600, 4: 250}
	Tranche      = map[string]map[int]int{
		"early": {1: 8200, 2: 2300},
		"mid":   {1: 6200, 2: 1700},
		"late":  {1: 3400, 2: 1100},
	}
)

// AgeBands are age-retention factors per position (from DYNASTY_PLAYBOOK.md).
var AgeBands = map[string][]AgeBand{
	"RB": {{25, 1.0}, {26, 0.85}, {27, 0.70}, {28, 0.50}, {29, 0.30}, {200, 0.15}},
	"WR": {{27, 1.0}, {28, 0.90}, {29, 0.75}, {30, 0.60}, {31, 0.40}, {200, 0.20}},
	"TE": {{28, 1.0}, {29, 0.90}, {30, 0.80}, {31, 0.65}, {32, 0.50}, {200, 0.30}},
	"QB": {{30, 1.0}, {31, 0.90}, {32, 0.80}, {33, 0.70}, {34, 0.55}, {200, 0.40}},
}

type AgeBand struct {
	MaxAge int
	Factor float64
}

// AgeFactor returns the age-retention factor for a position/age.
func AgeFactor(pos string, age *int) float64 {
	if age == nil {
		return 1.0
	}
	bands, ok := AgeBands[pos]
	if !ok {
		return 1.0
	}
	for _, b := range bands {
		if *age <= b.MaxAge {
			return b.Factor
		}
	}
	return bands[len(bands)-1].Factor
}
