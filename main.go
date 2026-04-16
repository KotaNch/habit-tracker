package main

import (
	"database/sql"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

type Habit struct {
	ID        int64
	Name      string
	CreatedAt string
	Streak    int
}

type Day struct {
	Date string
	Done bool
}

type HomePageData struct {
	Habits []Habit
}

type HabitPageData struct {
	Habit Habit
	Days  []Day
}

var (
	db  *sql.DB
	tpl *template.Template
)

func main() {
	var err error

	db, err = sql.Open("sqlite", "file:habit.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := initDB(); err != nil {
		log.Fatal(err)
	}

	tpl = template.Must(template.ParseFiles(
		"templates/habits.html",
		"templates/habit.html",
	))

	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/habit", habitHandler)
	http.HandleFunc("/habit/create", createHabitHandler)
	http.HandleFunc("/habit/toggle", toggleTodayHandler)

	log.Println("Server started on localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func initDB() error {
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return err
	}

	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS habits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS habit_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			habit_id INTEGER NOT NULL,
			day TEXT NOT NULL,
			checked_at TEXT NOT NULL,
			UNIQUE(habit_id, day),
			FOREIGN KEY (habit_id) REFERENCES habits(id) ON DELETE CASCADE
		);
	`)
	return err
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	habits, err := listHabits()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := HomePageData{Habits: habits}

	if err := tpl.ExecuteTemplate(w, "habits.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func habitHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	habit, err := getHabitByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	checks, err := getHabitChecks(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	habit.Streak = calcStreak(checks)

	data := HabitPageData{
		Habit: habit,
		Days:  buildDays(checks),
	}

	if err := tpl.ExecuteTemplate(w, "habit.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func createHabitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	_, err := db.Exec(
		`INSERT INTO habits (name, created_at) VALUES (?, ?)`,
		name,
		time.Now().Format(time.RFC3339),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)

}

func toggleTodayHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}

	today := time.Now().Format("2006-01-02")

	var exists int
	err = db.QueryRow(
		`SELECT 1 FROM habit_checks WHERE habit_id = ? AND day = ?`,
		id, today,
	).Scan(&exists)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = db.Exec(
			`INSERT INTO habit_checks (habit_id, day, checked_at) VALUES (?, ?, ?)`,
			id, today, time.Now().Format(time.RFC3339),
		)
	} else {
		_, err = db.Exec(
			`DELETE FROM habit_checks WHERE habit_id = ? AND day = ?`,
			id, today,
		)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/habit?id="+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func listHabits() ([]Habit, error) {
	rows, err := db.Query(
		`SELECT id, name, created_at FROM habits ORDER BY id DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var habits []Habit
	for rows.Next() {
		var h Habit
		if err := rows.Scan(&h.ID, &h.Name, &h.CreatedAt); err != nil {
			return nil, err
		}
		checks, err := getHabitChecks(h.ID)
		if err != nil {
			return nil, err
		}
		h.Streak = calcStreak(checks)
		habits = append(habits, h)

	}
	return habits, rows.Err()
}
func getHabitChecks(habitID int64) (map[string]bool, error) {
	rows, err := db.Query(
		`SELECT day FROM habit_checks WHERE habit_id = ?`,
		habitID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	checks := make(map[string]bool)
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return nil, err

		}
		checks[day] = true
	}
	return checks, rows.Err()
}

func calcStreak(checks map[string]bool) int {
	s := 0
	d := time.Now()

	for i := 0; i < 365; i++ {
		k := d.Format("2006-01-02")
		if checks[k] {
			s++
			d = d.AddDate(0, 0, -1)
			continue
		}
		break
	}
	return s
}
func buildDays(checks map[string]bool) []Day {
	days := make([]Day, 0, 365)
	s := time.Now().AddDate(0, 0, -364)

	for i := 0; i < 365; i++ {
		d := s.AddDate(0, 0, i)
		k := d.Format("2006-01-02")
		days = append(days, Day{
			Date: k,
			Done: checks[k],
		})
	}
	return days
}

func getHabitByID(id int64) (Habit, error) {
	var h Habit
	err := db.QueryRow(
		`SELECT id, name, created_at FROM habits WHERE id = ?`,
		id,
	).Scan(&h.ID, &h.Name, &h.CreatedAt)

	return h, err
}
