package db

import (
	"database/sql"
	"log/slog"

	_ "modernc.org/sqlite"
)

var DB *sql.DB
var Logger *slog.Logger

func InitDB(logger *slog.Logger) error {
	var err error
	DB, err = sql.Open("sqlite", "file:habit.db")
	if err != nil {
		return err
	}
	if err := createTables(); err != nil {
		return err
	}
	Logger = logger
	return nil
}

func createTables() error {
	if _, err := DB.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return err
	}

	_, err := DB.Exec(`
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
	return err
}
