package points

import "testing"

// Gleichstand teilt sich einen Platz. Zwei Zweite, danach ein Vierter — so
// steht es auf jedem Sportplatz und so erwartet es auch ein Kind.
func TestPlatzierenTeiltGleichstand(t *testing.T) {
	raenge := []Monatsrang{
		{Name: "Anna", Points: 90},
		{Name: "Ben", Points: 40},
		{Name: "Clara", Points: 40},
		{Name: "David", Points: 10},
	}
	platzieren(raenge)

	erwartet := []int{1, 2, 2, 4}
	for i, want := range erwartet {
		if raenge[i].Rank != want {
			t.Errorf("%s: erwartet Platz %d, war %d", raenge[i].Name, want, raenge[i].Rank)
		}
	}
}

// Auch ohne Punkte braucht jeder einen Platz — sonst steht in der Rangliste
// eine Zeile ohne Zahl.
func TestPlatzierenOhnePunkte(t *testing.T) {
	raenge := []Monatsrang{{Name: "Anna"}, {Name: "Ben"}}
	platzieren(raenge)
	if raenge[0].Rank != 1 || raenge[1].Rank != 1 {
		t.Errorf("bei Gleichstand 0:0 erwartet zweimal Platz 1, war %d und %d",
			raenge[0].Rank, raenge[1].Rank)
	}
}

func TestMonatsLabel(t *testing.T) {
	if got := monatsLabel("2026-09"); got != "September 2026" {
		t.Errorf("erwartet September 2026, war %q", got)
	}
	if got := monatsLabel("2026-03"); got != "März 2026" {
		t.Errorf("erwartet März 2026, war %q", got)
	}
	// Unlesbares bleibt stehen, statt eine Panik auszulösen.
	if got := monatsLabel("kaputt"); got != "kaputt" {
		t.Errorf("unlesbarer Monat sollte unverändert bleiben, war %q", got)
	}
}
