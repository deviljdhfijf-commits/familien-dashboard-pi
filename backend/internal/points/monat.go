package points

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"family-dashboard/backend/internal/auth"
	"github.com/rs/zerolog/log"
)

// Der Monatsabschluss.
//
// Die laufende Rangliste rechnet jedes Mal neu aus point_events. Das ist
// richtig, solange der Monat läuft — und falsch, sobald er vorbei ist: Wer
// im September gewonnen hat, soll im Dezember noch dastehen, auch wenn die
// einmaligen Aufgaben von damals längst gelöscht sind.
//
// Deshalb wird jeder abgeschlossene Monat einmal festgeschrieben und danach
// nicht mehr angefasst. Name, Farbe und Emoji werden mitkopiert: Sie gehören
// zur Platzierung von damals.

// Monatsrang ist eine Zeile der festgeschriebenen Rangliste.
type Monatsrang struct {
	UserID      int    `json:"user_id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	AvatarEmoji string `json:"avatar_emoji"`
	Points      int    `json:"points"`
	Activities  int    `json:"activities"`
	Rank        int    `json:"rank"`
}

// Monat bündelt die Rangliste eines Monats.
type Monat struct {
	// Monat als JJJJ-MM, Label als „September 2026".
	Monat  string       `json:"month"`
	Label  string       `json:"label"`
	Raenge []Monatsrang `json:"ranks"`
	// Laufend ist wahr für den Monat, der gerade läuft. Seine Zahlen ändern
	// sich noch — die Oberfläche soll das sagen dürfen.
	Laufend bool `json:"running"`
}

var monatsnamen = [...]string{
	"Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember",
}

// monatsLabel macht aus 2026-09 ein „September 2026".
func monatsLabel(monat string) string {
	t, err := time.Parse("2006-01", monat)
	if err != nil {
		return monat
	}
	return monatsnamen[int(t.Month())-1] + " " + t.Format("2006")
}

// Start schreibt beim Hochfahren alles fest, was abgeschlossen ist, und sieht
// danach zweimal am Tag nach. Öfter braucht es nicht: Ein Monatswechsel ist
// kein Ereignis, das auf die Minute genau erwischt werden muss, und ein
// verpasster Wechsel wird beim nächsten Durchlauf nachgeholt.
func (s *Service) Start(ctx context.Context) {
	s.Monatsabschluss()
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Monatsabschluss()
		}
	}
}

// Monatsabschluss schreibt jeden vergangenen Monat fest, der noch fehlt.
//
// Nachträglich, nicht zum Monatswechsel: Ein Dashboard im Flur kann über
// Silvester ausgeschaltet sein. Was zählt, ist dass am Ende jeder Monat
// genau einmal in der Tabelle steht.
func (s *Service) Monatsabschluss() {
	monate, err := s.offeneMonate()
	if err != nil {
		log.Error().Err(err).Msg("Monatsabschluss: offene Monate nicht lesbar")
		return
	}
	for _, monat := range monate {
		if err := s.monatFestschreiben(monat); err != nil {
			log.Error().Err(err).Str("monat", monat).Msg("Monatsabschluss fehlgeschlagen")
			continue
		}
		log.Info().Str("monat", monat).Msg("Rangliste des Monats festgeschrieben")
	}
}

// offeneMonate sind die Monate mit Punkten, die vorbei sind und noch nicht
// in der Tabelle stehen.
func (s *Service) offeneMonate() ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT strftime('%Y-%m', created_at) AS monat
		FROM point_events
		WHERE strftime('%Y-%m', created_at) < strftime('%Y-%m', 'now', 'localtime')
		  AND strftime('%Y-%m', created_at) NOT IN (SELECT month FROM month_scores)
		ORDER BY monat`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var monate []string
	for rows.Next() {
		var m sql.NullString
		if err := rows.Scan(&m); err != nil || !m.Valid || m.String == "" {
			continue
		}
		monate = append(monate, m.String)
	}
	return monate, rows.Err()
}

func (s *Service) monatFestschreiben(monat string) error {
	rows, err := s.db.Query(`
		SELECT p.user_id, u.name, u.color, u.avatar_emoji,
		       COALESCE(SUM(p.points), 0), COUNT(p.id)
		FROM point_events p
		JOIN users u ON u.id = p.user_id
		WHERE strftime('%Y-%m', p.created_at) = ?
		GROUP BY p.user_id
		ORDER BY 5 DESC, u.name ASC`, monat)
	if err != nil {
		return err
	}
	defer rows.Close()

	var raenge []Monatsrang
	for rows.Next() {
		var r Monatsrang
		if err := rows.Scan(&r.UserID, &r.Name, &r.Color, &r.AvatarEmoji,
			&r.Points, &r.Activities); err != nil {
			return err
		}
		raenge = append(raenge, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(raenge) == 0 {
		return nil
	}
	platzieren(raenge)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range raenge {
		if _, err := tx.Exec(`
			INSERT INTO month_scores (month, user_id, name, color, avatar_emoji, points, activities, rank)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(month, user_id) DO NOTHING`,
			monat, r.UserID, r.Name, r.Color, r.AvatarEmoji, r.Points, r.Activities, r.Rank); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// platzieren vergibt die Plätze. Gleichstand teilt sich einen Platz — zwei
// Kinder mit 40 Punkten sind beide Zweiter, und der nächste ist Vierter.
func platzieren(raenge []Monatsrang) {
	platz, vorher := 0, -1
	for i := range raenge {
		if raenge[i].Points != vorher {
			platz = i + 1
			vorher = raenge[i].Points
		}
		raenge[i].Rank = platz
	}
}

// ------------------------------------------------------------------ Handler

// Months liefert die festgeschriebenen Ranglisten, neueste zuerst, und davor
// den laufenden Monat. Der laufende ist als solcher gekennzeichnet: Seine
// Zahlen sind ein Zwischenstand, kein Ergebnis.
func (s *Service) Months(w http.ResponseWriter, r *http.Request) {
	// Ein Blick auf die Seite ist ein guter Moment, um nachzusehen, ob
	// inzwischen ein Monat zu Ende gegangen ist.
	s.Monatsabschluss()

	laufend, err := s.laufenderMonat()
	if err != nil {
		log.Error().Err(err).Msg("Laufender Monat nicht lesbar")
		auth.HTTPError(w, http.StatusInternalServerError, "Server-Fehler")
		return
	}

	monate := []Monat{}
	if len(laufend.Raenge) > 0 {
		monate = append(monate, laufend)
	}

	abgeschlossen, err := s.abgeschlosseneMonate()
	if err != nil {
		log.Error().Err(err).Msg("Monatsranglisten nicht lesbar")
		auth.HTTPError(w, http.StatusInternalServerError, "Server-Fehler")
		return
	}
	monate = append(monate, abgeschlossen...)
	auth.WriteJSON(w, monate)
}

func (s *Service) laufenderMonat() (Monat, error) {
	monat := time.Now().Format("2006-01")
	rows, err := s.db.Query(`
		SELECT p.user_id, u.name, u.color, u.avatar_emoji,
		       COALESCE(SUM(p.points), 0), COUNT(p.id)
		FROM point_events p
		JOIN users u ON u.id = p.user_id
		WHERE strftime('%Y-%m', p.created_at) = ?
		GROUP BY p.user_id
		ORDER BY 5 DESC, u.name ASC`, monat)
	if err != nil {
		return Monat{}, err
	}
	defer rows.Close()

	var raenge []Monatsrang
	for rows.Next() {
		var r Monatsrang
		if err := rows.Scan(&r.UserID, &r.Name, &r.Color, &r.AvatarEmoji,
			&r.Points, &r.Activities); err != nil {
			return Monat{}, err
		}
		raenge = append(raenge, r)
	}
	if err := rows.Err(); err != nil {
		return Monat{}, err
	}
	platzieren(raenge)
	return Monat{Monat: monat, Label: monatsLabel(monat), Raenge: raenge, Laufend: true}, nil
}

func (s *Service) abgeschlosseneMonate() ([]Monat, error) {
	rows, err := s.db.Query(`
		SELECT month, user_id, name, color, avatar_emoji, points, activities, rank
		FROM month_scores
		ORDER BY month DESC, rank ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var monate []Monat
	for rows.Next() {
		var monat string
		var r Monatsrang
		if err := rows.Scan(&monat, &r.UserID, &r.Name, &r.Color, &r.AvatarEmoji,
			&r.Points, &r.Activities, &r.Rank); err != nil {
			return nil, err
		}
		if len(monate) == 0 || monate[len(monate)-1].Monat != monat {
			monate = append(monate, Monat{Monat: monat, Label: monatsLabel(monat)})
		}
		letzter := &monate[len(monate)-1]
		letzter.Raenge = append(letzter.Raenge, r)
	}
	return monate, rows.Err()
}
