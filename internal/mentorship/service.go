package mentorship

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"upskill/internal/auth"
	"upskill/internal/web"
)

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db: db} }

func (s *Service) Routes() http.Handler {
	r := chi.NewRouter()
	return r
}

func (s *Service) RequestCreate(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	var in struct {
		MentorID int64  `json:"mentorId"`
		Message  string `json:"message"`
	}
	if err := web.DecodeJSON(r, &in); err != nil || in.MentorID <= 0 {
		http.Error(w, "bad input", 400)
		return
	}
	var exists bool
	if err := s.db.QueryRow(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM mentorships WHERE student_id=$1 AND mentor_id=$2 AND status='active')
	`, uid, in.MentorID).Scan(&exists); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if exists {
		http.Error(w, "already active", 409)
		return
	}
	var id int64
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO mentorship_requests(student_id, mentor_id, message, status)
		VALUES($1,$2,$3,'pending') RETURNING id
	`, uid, in.MentorID, in.Message).Scan(&id)
	if err != nil {
		http.Error(w, "duplicate pending?", 409)
		return
	}
	web.JSON(w, 201, map[string]any{"requestId": id, "status": "pending"})
}

func (s *Service) MyRequests(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT id, mentor_id, message, status, created_at, decided_at
		FROM mentorship_requests WHERE student_id=$1
		ORDER BY created_at DESC
	`, uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	type Req struct {
		ID        int64      `json:"id"`
		MentorID  int64      `json:"mentorId"`
		Message   string     `json:"message"`
		Status    string     `json:"status"`
		CreatedAt time.Time  `json:"createdAt"`
		DecidedAt *time.Time `json:"decidedAt,omitempty"`
	}
	var items []Req
	for rows.Next() {
		var it Req
		var decidedAt *time.Time
		if err := rows.Scan(&it.ID, &it.MentorID, &it.Message, &it.Status, &it.CreatedAt, &decidedAt); err == nil {
			it.DecidedAt = decidedAt
			items = append(items, it)
		}
	}
	web.JSON(w, 200, map[string]any{"items": items})
}

func (s *Service) MentorRequests(w http.ResponseWriter, r *http.Request) {
	mid := auth.UserID(r)
	status := web.QueryString(r, "status", "")
	q := `
		SELECT mr.id, mr.student_id, COALESCE(u.first_name,'')||' '||COALESCE(u.last_name,'') as student_name,
		       mr.message, mr.status, mr.created_at
		FROM mentorship_requests mr
		LEFT JOIN users u ON u.id = mr.student_id
		WHERE mr.mentor_id=$1
	`
	if status != "" {
		q += " AND mr.status = '" + status + "'"
	}
	q += " ORDER BY mr.created_at DESC"
	rows, err := s.db.Query(r.Context(), q, mid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	type Req struct {
		ID        int64     `json:"id"`
		StudentID int64     `json:"studentId"`
		Student   string    `json:"student"`
		Message   string    `json:"message"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"createdAt"`
	}
	var items []Req
	for rows.Next() {
		var it Req
		if err := rows.Scan(&it.ID, &it.StudentID, &it.Student, &it.Message, &it.Status, &it.CreatedAt); err == nil {
			items = append(items, it)
		}
	}
	web.JSON(w, 200, map[string]any{"items": items})
}

func (s *Service) Approve(w http.ResponseWriter, r *http.Request) {
	mid := auth.UserID(r)
	reqID, err := web.ParamInt64(r, "id")
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())

	var studentID int64
	var status string
	if err := tx.QueryRow(r.Context(), `
		SELECT student_id, status FROM mentorship_requests WHERE id=$1 AND mentor_id=$2
	`, reqID, mid).Scan(&studentID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	if status != "pending" {
		http.Error(w, "bad state", 409)
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE mentorship_requests SET status='approved', decided_at=now() WHERE id=$1
	`, reqID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO mentorships(student_id, mentor_id, status)
		VALUES($1,$2,'active')
		ON CONFLICT (student_id, mentor_id) WHERE status='active' DO NOTHING
	`, studentID, mid); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO conversations(student_id, mentor_id)
		VALUES($1,$2)
		ON CONFLICT (student_id, mentor_id) DO NOTHING
	`, studentID, mid); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	web.JSON(w, 200, map[string]any{"ok": true})
}

func (s *Service) Decline(w http.ResponseWriter, r *http.Request) {
	mid := auth.UserID(r)
	reqID, err := web.ParamInt64(r, "id")
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}

	res, err := s.db.Exec(r.Context(), `
		UPDATE mentorship_requests SET status='declined', decided_at=now()
		WHERE id=$1 AND mentor_id=$2 AND status='pending'
	`, reqID, mid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if res.RowsAffected() == 0 {
		http.Error(w, "not found or bad state", 409)
		return
	}
	web.JSON(w, 200, map[string]any{"ok": true})
}

func (s *Service) ListMentees(w http.ResponseWriter, r *http.Request) {
	mid := auth.UserID(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT m.student_id,
		       COALESCE(u.first_name,'')||' '||COALESCE(u.last_name,'') as name,
		       COALESCE(c.id,0) as conversation_id,
		       m.created_at
		FROM mentorships m
		LEFT JOIN users u ON u.id = m.student_id
		LEFT JOIN conversations c ON c.student_id = m.student_id AND c.mentor_id = m.mentor_id
		WHERE m.mentor_id=$1 AND m.status='active'
		ORDER BY m.created_at DESC
	`, mid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	type Item struct {
		StudentID      int64     `json:"studentId"`
		Name           string    `json:"name"`
		ConversationID int64     `json:"conversationId"`
		Since          time.Time `json:"since"`
	}
	var items []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.StudentID, &it.Name, &it.ConversationID, &it.Since); err == nil {
			items = append(items, it)
		}
	}
	web.JSON(w, 200, map[string]any{"items": items})
}

func (s *Service) ListMentors(w http.ResponseWriter, r *http.Request) {
	sid := auth.UserID(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT m.mentor_id,
		       COALESCE(u.first_name,'')||' '||COALESCE(u.last_name,'') as name,
		       COALESCE(c.id,0) as conversation_id,
		       m.created_at
		FROM mentorships m
		LEFT JOIN users u ON u.id = m.mentor_id
		LEFT JOIN conversations c ON c.student_id = m.student_id AND c.mentor_id = m.mentor_id
		WHERE m.student_id=$1 AND m.status='active'
		ORDER BY m.created_at DESC
	`, sid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	type Item struct {
		MentorID       int64     `json:"mentorId"`
		Name           string    `json:"name"`
		ConversationID int64     `json:"conversationId"`
		Since          time.Time `json:"since"`
	}
	var items []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.MentorID, &it.Name, &it.ConversationID, &it.Since); err == nil {
			items = append(items, it)
		}
	}
	web.JSON(w, 200, map[string]any{"items": items})
}
