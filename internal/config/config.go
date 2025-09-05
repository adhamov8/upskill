package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Env            string
	Port           int
	DatabaseURL    string
	AllowedOrigins []string

	// JWT
	JWTPrivatePEM string
	JWTPublicPEM  string

	// Google Login
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	CalendarRedirectURL string
	CalendarEnabled     bool
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func Load() Config {
	port, _ := strconv.Atoi(getenv("APP_PORT", "8000"))
	cors := getenv("CORS_ALLOWED_ORIGINS", "http://localhost:5173")
	calEnabled := getenv("GOOGLE_CALENDAR_ENABLED", "0") == "1"

	cfg := Config{
		Env:                 getenv("APP_ENV", "dev"),
		Port:                port,
		DatabaseURL:         getenv("DATABASE_URL", "postgres://upskill:upskill@localhost:5432/upskill?sslmode=disable"),
		AllowedOrigins:      strings.Split(cors, ","),
		JWTPrivatePEM:       os.Getenv("JWT_PRIVATE_PEM"),
		JWTPublicPEM:        os.Getenv("JWT_PUBLIC_PEM"),
		GoogleClientID:      os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:  os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:   getenv("GOOGLE_REDIRECT_URL", "http://localhost:8000/api/auth/google/callback"),
		CalendarRedirectURL: getenv("GOOGLE_CALENDAR_REDIRECT_URL", "http://localhost:8000/api/integrations/google/calendar/callback"),
		CalendarEnabled:     calEnabled,
	}
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	return cfg
}
