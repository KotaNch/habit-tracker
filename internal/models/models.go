package models

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
	Lang   string
}

type HabitPageData struct {
	Habit Habit
	Days  []Day
	Lang  string
}
