package models

// NFLTeams maps a team's standard abbreviation to its full name.
var NFLTeams = map[string]string{
	"ARI": "Arizona Cardinals", "ATL": "Atlanta Falcons", "BAL": "Baltimore Ravens",
	"BUF": "Buffalo Bills", "CAR": "Carolina Panthers", "CHI": "Chicago Bears",
	"CIN": "Cincinnati Bengals", "CLE": "Cleveland Browns", "DAL": "Dallas Cowboys",
	"DEN": "Denver Broncos", "DET": "Detroit Lions", "GB": "Green Bay Packers",
	"HOU": "Houston Texans", "IND": "Indianapolis Colts", "JAX": "Jacksonville Jaguars",
	"KC": "Kansas City Chiefs", "LV": "Las Vegas Raiders", "LAC": "Los Angeles Chargers",
	"LAR": "Los Angeles Rams", "MIA": "Miami Dolphins", "MIN": "Minnesota Vikings",
	"NE": "New England Patriots", "NO": "New Orleans Saints", "NYG": "New York Giants",
	"NYJ": "New York Jets", "PHI": "Philadelphia Eagles", "PIT": "Pittsburgh Steelers",
	"SF": "San Francisco 49ers", "SEA": "Seattle Seahawks", "TB": "Tampa Bay Buccaneers",
	"TEN": "Tennessee Titans", "WAS": "Washington Commanders",
}

var nflTeamAbbrByName = func() map[string]string {
	m := make(map[string]string, len(NFLTeams))
	for abbr, name := range NFLTeams {
		m[name] = abbr
	}
	return m
}()

// TeamAbbrForName returns the abbreviation for a full NFL team name (e.g.
// "Philadelphia Eagles" -> "PHI"), and whether it matched.
func TeamAbbrForName(name string) (string, bool) {
	abbr, ok := nflTeamAbbrByName[name]
	return abbr, ok
}
