package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"upskill/internal/config"
	"upskill/internal/web"
)

func New(cfg config.Config, pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()

	r.Use(web.RequestID)
	r.Use(web.Logger)
	r.Use(web.Recoverer)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           int((24 * time.Hour).Seconds()),
	}))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		web.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	api := newAPI(cfg, pool)
	r.Mount("/api", api)

	_ = strings.Builder{}
	return r
}
