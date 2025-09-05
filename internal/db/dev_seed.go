package db

import (
	"context"
	"log"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func RunDevSeed(pool *pgxpool.Pool) error {
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	type U struct{ Email, First, Last, Pass string }
	users := []U{
		{"student@example.com", "Student", "Demo", "password"},
		{"mentor@example.com", "Mentor", "Demo", "password"},
	}
	ids := make(map[string]int64)
	for _, u := range users {
		var id int64
		hash, _ := bcrypt.GenerateFromPassword([]byte(u.Pass), bcrypt.DefaultCost)
		err := tx.QueryRow(ctx, `
			INSERT INTO users(email, password_hash, first_name, last_name)
			VALUES($1,$2,$3,$4)
			ON CONFLICT (email) DO UPDATE SET first_name=EXCLUDED.first_name
			RETURNING id
		`, strings.ToLower(u.Email), string(hash), u.First, u.Last).Scan(&id)
		if err != nil {
			log.Printf("seed user %s: %v", u.Email, err)
			continue
		}
		ids[u.Email] = id
	}

	_, _ = tx.Exec(ctx, `INSERT INTO user_roles(user_id, role) VALUES($1,'student') ON CONFLICT DO NOTHING`, ids["student@example.com"])
	_, _ = tx.Exec(ctx, `INSERT INTO user_roles(user_id, role) VALUES($1,'mentor') ON CONFLICT DO NOTHING`, ids["mentor@example.com"])

	_, _ = tx.Exec(ctx, `
		INSERT INTO mentorships(student_id, mentor_id, status)
		VALUES($1,$2,'active')
		ON CONFLICT (student_id, mentor_id) WHERE status='active' DO NOTHING
	`, ids["student@example.com"], ids["mentor@example.com"])
	_, _ = tx.Exec(ctx, `
		INSERT INTO conversations(student_id, mentor_id)
		VALUES($1,$2) ON CONFLICT DO NOTHING
	`, ids["student@example.com"], ids["mentor@example.com"])

	return tx.Commit(ctx)
}
