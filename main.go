package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
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
	Locale map[string]string
}

type HabitPageData struct {
	Habit  Habit
	Days   []Day
	Locale map[string]string
}

var (
	db  *sql.DB
	tpl *template.Template
)

var logger *slog.Logger

var (
	oauthConfig  *oauth2.Config
	sessionStore = struct {
		sync.RWMutex
		data map[string]int64
	}{data: make(map[string]int64)}
)

func initOAuth() {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		log.Fatal("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set")
	}
	oauthConfig = &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  "https://habit-tracker.ddns.net/auth/google/callback",
		Scopes:       []string{"https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile"},
		Endpoint:     google.Endpoint,
	}
}

func generateStateCookie() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func init() {
	hanler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger = slog.New(hanler)
}

func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWritter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lw, r)
		duration := time.Since(start)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lw.statusCode,
			"duration_ms", duration.Microseconds(),
			"remote_addr", r.RemoteAddr,
		)
	}
}

type loggingResponseWritter struct {
	http.ResponseWriter
	statusCode int
}

func (lw *loggingResponseWritter) WriteHeader(code int) {
	lw.statusCode = code
	lw.ResponseWriter.WriteHeader(code)
}

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
	if err := loadTranslations(); err != nil {
		log.Fatal(err)
	}

	tpl = template.Must(template.ParseFiles(
		"templates/habits.html",
		"templates/habit.html",
	))
	initOAuth()

	http.HandleFunc("/", languageMiddleware(authMiddleware(homeHandler)))
	http.HandleFunc("/habit", languageMiddleware(authMiddleware(habitHandler)))
	http.HandleFunc("/habit/create", languageMiddleware(authMiddleware(createHabitHandler)))
	http.HandleFunc("/habit/toggle", languageMiddleware(authMiddleware(toggleTodayHandler)))
	http.HandleFunc("/habit/delete", languageMiddleware(authMiddleware(deleteHabitHandler)))
	http.HandleFunc("/login", languageMiddleware(loginHandler))
	http.HandleFunc("/auth/google/callback", languageMiddleware(googleCallbackHandler))
	http.HandleFunc("/logout", languageMiddleware(logoutHandler))

	log.Println("Server started on localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func initDB() error {
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return err
	}

	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		google_sub TEXT UNIQUE NOT NULL,
		email TEXT NOT NULL,
		name TEXT,
		created_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS habits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
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
	if err != nil {
		logger.Error("failed to create tables", err)
	} else {
		logger.Info("database tables ready")
	}
	return err
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(int64)
	lang := r.Context().Value("lang").(string)
	habits, err := listHabits(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := HomePageData{Habits: habits, Locale: translations[lang]}

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
	userID := r.Context().Value("userID").(int64)
	habit, err := getHabitByID(id, userID)
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
	lang := r.Context().Value("lang").(string)

	habit.Streak = calcStreak(checks)

	data := HabitPageData{
		Habit:  habit,
		Days:   buildDays(checks),
		Locale: translations[lang],
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
		logger.Warn("attempt to create habit with empty name")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	userID := r.Context().Value("userID").(int64)
	_, err := db.Exec(
		`INSERT INTO habits (user_id, name, created_at) VALUES (?, ?, ?)`,
		userID, name, time.Now().Format(time.RFC3339),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logger.Info("habit created", "name", name)
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

	date := r.FormValue("date")
	if date == "" {
		http.Error(w, "Missing date", http.StatusBadRequest)
		return
	}

	if _, err := time.Parse("2006-01-02", date); err != nil {
		http.Error(w, "Invalid format of date", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value("userID").(int64)

	var ownerID int64
	err = db.QueryRow(`SELECT user_id FROM habits WHERE id = ?`, id).Scan(&ownerID)
	if err != nil || ownerID != userID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var exists int
	err = db.QueryRow(
		`SELECT 1 FROM habit_checks WHERE habit_id = ? AND day = ?`,
		id, date,
	).Scan(&exists)

	var done bool
	if errors.Is(err, sql.ErrNoRows) {
		_, err = db.Exec(`INSERT INTO habit_checks (habit_id, day, checked_at) VALUES (?, ?, ?)`,
			id, date, time.Now().Format(time.RFC3339))
		done = true
		logger.Info("habit checked", "habit_id", id, "day", date)
	} else if err == nil {
		_, err = db.Exec(`DELETE FROM habit_checks WHERE habit_id = ? AND day = ?`, id, date)
		logger.Info("habit unchecked", "habit_id", id, "day", date)
	} else {
		logger.Error("toggle check query failed", "habit_id", id, "date", date, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	checks, err := getHabitChecks(id)
	if err != nil {
		logger.Error("failed to get checks")
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	streak := calcStreak(checks)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"done":    done,
		"streak":  streak,
	})
	return

	// http.Redirect(w, r, "/habit?id="+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func listHabits(userID int64) ([]Habit, error) {
	rows, err := db.Query(`SELECT id, name, created_at FROM habits WHERE user_id = ? ORDER BY id DESC`, userID)
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

	for {
		k := d.Format("2006-01-02")
		if checks[k] {
			s++
			d = d.AddDate(0, 0, -1)
		} else {
			break
		}

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

func getHabitByID(id, userID int64) (Habit, error) {
	var h Habit
	err := db.QueryRow(
		`SELECT id, name, created_at FROM habits WHERE id = ? AND user_id = ?`,

		id, userID,
	).Scan(&h.ID, &h.Name, &h.CreatedAt)

	return h, err
}

func deleteHabitHandler(w http.ResponseWriter, r *http.Request) {
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

	userID := r.Context().Value("userID").(int64)
	result, err := db.Exec(`DELETE FROM habits WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		logger.Error("failed to delete habit", "id", id, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.NotFound(w, r)
		return
	}

	logger.Info("habit deleted", "id", id)

	http.Redirect(w, r, "/", http.StatusSeeOther)

}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		sessionStore.RLock()
		userID, ok := sessionStore.data[cookie.Value]
		sessionStore.RUnlock()

		if !ok || userID == 0 {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		ctx := context.WithValue(r.Context(), "userID", userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	state := generateStateCookie()
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		HttpOnly: true,
		MaxAge:   60 * 10,
	})
	url := oauthConfig.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusFound)
}

func googleCallbackHandler(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	cookieState, _ := r.Cookie("oauth_state")
	if cookieState == nil || cookieState.Value != state {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	token, err := oauthConfig.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusInternalServerError)
		return
	}
	client := oauthConfig.Client(r.Context(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		http.Error(w, "user info failed", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var userInfo struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&userInfo)

	userID, err := findOrCreateUser(userInfo.ID, userInfo.Email, userInfo.Name)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	sessionID := uuid.New().String()
	sessionStore.Lock()
	sessionStore.data[sessionID] = userID
	sessionStore.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		HttpOnly: true,
		Path:     "/",
		MaxAge:   86400 * 7,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	cookie, err := r.Cookie("session_id")
	if err == nil && cookie.Value != "" {
		sessionStore.Lock()
		delete(sessionStore.data, cookie.Value)
		sessionStore.Unlock()

	}
	http.SetCookie(w, &http.Cookie{
		Name:   "session_id",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)

}

func findOrCreateUser(googleSub, email, name string) (int64, error) {
	var userID int64
	err := db.QueryRow(`SELECT id FROM users WHERE google_sub = ?`, googleSub).Scan(&userID)
	if err == nil {
		return userID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := db.Exec(`INSERT INTO users (google_sub, email, name, created_at) VALUES (?, ?, ?, ?)`,
		googleSub, email, name, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type Locale map[string]string

var translations map[string]Locale

func loadTranslations() error {
	translations = make(map[string]Locale)
	files, err := filepath.Glob("locales/*.json")
	if err != nil {
		return err
	}
	for _, f := range files {
		lang := strings.TrimSuffix(filepath.Base(f), ".json")
		data, err := ioutil.ReadFile(f)
		if err != nil {
			return err
		}

		var loc Locale
		if err := json.Unmarshal(data, &loc); err != nil {
			return err
		}
		translations[lang] = loc
	}
	return nil
}

func T(lang, key string, data map[string]interface{}) string {
	if loc, ok := translations[lang]; ok {
		tpl := loc[key]
		for k, v := range data {
			tpl = strings.ReplaceAll(tpl, "{{."+k+"}}", fmt.Sprintf("%v", v))
		}
		return tpl
	}
	return key
}

func languageMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang := r.URL.Query().Get("lang")
		if lang == "" {
			cookie, err := r.Cookie("lang")
			if err == nil && (cookie.Value == "ru" || cookie.Value == "en") {
				lang = cookie.Value
			} else {
				lang = "en"
			}
		}
		http.SetCookie(w, &http.Cookie{
			Name:   "lang",
			Value:  lang,
			Path:   "/",
			MaxAge: 86400 * 365,
		})
		ctx := context.WithValue(r.Context(), "lang", lang)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
