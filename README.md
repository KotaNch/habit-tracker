# Habit tracker

A simple web app for tracking habits.  
You can add habits, mark days as done, and see your streak.

## Tech Stack

- **Frontend:** HTML, CSS, JavaScript
- **Backend:** Go
- **Database:** SQLite
- **Authentication:** Google OAuth
- **Templates:** Go HTML templates

## Environment Variables

Before running the project, create a `.env` file or set these variables in your system:

```bash
GOOGLE_CLIENT_ID=your_google_client_id
GOOGLE_CLIENT_SECRET=your_google_client_secret
```
Installation

### 1. Clone the repository
```
git clone https://github.com/your-username/habit-tracker.git
cd habit-tracker
```
### 2. Install dependencies

Make sure you have Go installed, then run:
```
go mod tidy
```
### 3. Set up environment variables

Add your Google OAuth credentials:
```
GOOGLE_CLIENT_ID=your_google_client_id
GOOGLE_CLIENT_SECRET=your_google_client_secret
```
### 4. Run the project
```
go run main.go
```
### 5. Open in browser

Open:

http://localhost:8080


P.S.

I will soon add this project to my VPS, making it more convenient to use.