package roles

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"upskill/internal/auth"
	"upskill/internal/web"
)

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

func (s *Service) Assign(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	var in struct {
		Role string `json:"role"`
	}
	if err := web.DecodeJSON(r, &in); err != nil {
		http.Error(w, "bad input", http.StatusBadRequest)
		return
	}
	role := strings.ToLower(strings.TrimSpace(in.Role))
	if role != "student" && role != "mentor" {
		http.Error(w, "role must be student or mentor", http.StatusBadRequest)
		return
	}
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO user_roles(user_id, role)
		VALUES($1,$2)
		ON CONFLICT (user_id, role) DO NOTHING
	`, uid, role); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	roles, err := s.listRoles(r, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	web.JSON(w, http.StatusOK, map[string]any{"ok": true, "roles": roles})
}

func (s *Service) Me(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	roles, err := s.listRoles(r, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	web.JSON(w, http.StatusOK, map[string]any{"roles": roles})
}

func (s *Service) listRoles(r *http.Request, uid int64) ([]string, error) {
	rows, err := s.db.Query(r.Context(), `SELECT role FROM user_roles WHERE user_id=$1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []string
	for rows.Next() {
		var role string
		_ = rows.Scan(&role)
		res = append(res, role)
	}
	return res, nil
}
