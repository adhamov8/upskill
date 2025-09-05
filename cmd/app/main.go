package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"upskill/internal/config"
	"upskill/internal/db"
	"upskill/internal/server"
)

func main() {
	cfg := config.Load()
	pool := db.MustConnect(cfg.DatabaseURL)
	defer pool.Close()

	if err := db.RunMigrations(pool); err != nil {
		log.Fatalf("migrations failed: %v", err)
	}

	if cfg.Env == "dev" && os.Getenv("DEMO_SEED") == "1" {
		if err := db.RunDevSeed(pool); err != nil {
			log.Printf("dev-seed error: %v", err)
		} else {
			log.Printf("dev-seed: OK")
		}
	}

	srv := server.New(cfg, pool)

	addr := ":" + strconv.Itoa(cfg.Port)
	log.Printf("UpSkill listening on %s (env=%s)", addr, cfg.Env)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
