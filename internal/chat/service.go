package chat

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"upskill/internal/auth"
	"upskill/internal/web"
)

type Service struct {
	db       *pgxpool.Pool
	hub      *Hub
	upgrader websocket.Upgrader
	auth     *auth.Service
}

func NewService(db *pgxpool.Pool, authSvc *auth.Service) *Service {
	return &Service{
		db:       db,
		hub:      NewHub(),
		auth:     authSvc,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
}

func (s *Service) GlobalHistory(w http.ResponseWriter, r *http.Request) {
	limit := web.QueryInt(r, "limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT gm.id, gm.author_id, COALESCE(u.first_name,'')||' '||COALESCE(u.last_name,'') as author,
		       gm.body, gm.created_at
		FROM global_messages gm
		LEFT JOIN users u ON u.id = gm.author_id
		ORDER BY gm.created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	type Msg struct {
		ID        int64     `json:"id"`
		AuthorID  int64     `json:"authorId"`
		Author    string    `json:"author"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"createdAt"`
	}
	var items []Msg
	for rows.Next() {
		var m Msg
		if err := rows.Scan(&m.ID, &m.AuthorID, &m.Author, &m.Body, &m.CreatedAt); err == nil {
			items = append(items, m)
		}
	}
	web.JSON(w, 200, map[string]any{"items": items})
}

func (s *Service) GlobalPost(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	var ok bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id=$1 AND role IN ('student','mentor'))`, uid).Scan(&ok); err != nil || !ok {
		http.Error(w, "forbidden", 403)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := web.DecodeJSON(r, &in); err != nil || in.Body == "" {
		http.Error(w, "bad body", 400)
		return
	}
	var id int64
	if err := s.db.QueryRow(r.Context(), `INSERT INTO global_messages(author_id, body) VALUES($1,$2) RETURNING id`, uid, in.Body).Scan(&id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	payload := map[string]any{"type": "message", "scope": "global", "id": id, "authorId": uid, "body": in.Body, "createdAt": time.Now().UTC()}
	s.hub.Broadcast("global", payload)
	web.JSON(w, 200, payload)
}

func (s *Service) GlobalWS(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	var ok bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id=$1 AND role IN ('student','mentor'))`, uid).Scan(&ok); err != nil || !ok {
		http.Error(w, "forbidden", 403)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	room := "global"
	s.hub.Join(room, conn)
	defer func() { s.hub.Leave(room, conn); conn.Close() }()

	// simple read loop to drain client pings; no specific incoming messages for global
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

type conversation struct {
	ID        int64
	StudentID int64
	MentorID  int64
}

func (s *Service) ListConversations(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT c.id, c.student_id, c.mentor_id,
		       CASE WHEN c.student_id=$1 THEN COALESCE(mu.first_name,'')||' '||COALESCE(mu.last_name,'')
		            ELSE COALESCE(su.first_name,'')||' '||COALESCE(su.last_name,'') END as peer_name
		FROM conversations c
		LEFT JOIN users su ON su.id = c.student_id
		LEFT JOIN users mu ON mu.id = c.mentor_id
		WHERE c.student_id=$1 OR c.mentor_id=$1
		ORDER BY c.id DESC
	`, uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	type Item struct {
		ID        int64  `json:"id"`
		StudentID int64  `json:"studentId"`
		MentorID  int64  `json:"mentorId"`
		Peer      string `json:"peer"`
	}
	var items []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.StudentID, &it.MentorID, &it.Peer); err == nil {
			items = append(items, it)
		}
	}
	web.JSON(w, 200, map[string]any{"items": items})
}

func (s *Service) EnsureConversation(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	var in struct {
		MentorID  *int64 `json:"mentorId,omitempty"`
		StudentID *int64 `json:"studentId,omitempty"`
	}
	if err := web.DecodeJSON(r, &in); err != nil || (in.MentorID == nil && in.StudentID == nil) {
		http.Error(w, "bad input", 400)
		return
	}
	var sid, mid int64
	if in.MentorID != nil {
		sid, mid = uid, *in.MentorID
	} else {
		sid, mid = *in.StudentID, uid
	}
	var active bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM mentorships WHERE student_id=$1 AND mentor_id=$2 AND status='active')`, sid, mid).Scan(&active); err != nil || !active {
		http.Error(w, "no active mentorship", 403)
		return
	}
	var convID int64
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO conversations(student_id, mentor_id)
		VALUES($1,$2)
		ON CONFLICT (student_id, mentor_id) DO UPDATE SET student_id=EXCLUDED.student_id
		RETURNING id
	`, sid, mid).Scan(&convID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	web.JSON(w, 200, map[string]any{"conversationId": convID})
}

func (s *Service) History(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	convID, err := web.ParamInt64(r, "id")
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	if ok, _ := s.isMember(r, convID, uid); !ok {
		http.Error(w, "forbidden", 403)
		return
	}
	limit := web.QueryInt(r, "limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, author_id, author_type, body, created_at
		FROM messages WHERE conversation_id=$1
		ORDER BY created_at DESC
		LIMIT $2
	`, convID, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	type Msg struct {
		ID         int64     `json:"id"`
		AuthorID   int64     `json:"authorId"`
		AuthorType string    `json:"authorType"`
		Body       string    `json:"body"`
		CreatedAt  time.Time `json:"createdAt"`
	}
	var items []Msg
	for rows.Next() {
		var m Msg
		if err := rows.Scan(&m.ID, &m.AuthorID, &m.AuthorType, &m.Body, &m.CreatedAt); err == nil {
			items = append(items, m)
		}
	}
	web.JSON(w, 200, map[string]any{"items": items})
}

func (s *Service) PostMessage(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	convID, err := web.ParamInt64(r, "id")
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	conv, err := s.getConversation(r, convID)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	atype := ""
	switch uid {
	case conv.StudentID:
		atype = "student"
	case conv.MentorID:
		atype = "mentor"
	default:
		http.Error(w, "forbidden", 403)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := web.DecodeJSON(r, &in); err != nil || in.Body == "" {
		http.Error(w, "bad body", 400)
		return
	}
	var msgID int64
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO messages(conversation_id, author_id, author_type, body)
		VALUES($1,$2,$3,$4) RETURNING id
	`, convID, uid, atype, in.Body).Scan(&msgID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	payload := map[string]any{
		"type": "message", "conversationId": convID, "id": msgID, "authorId": uid, "authorType": atype, "body": in.Body, "createdAt": time.Now().UTC(),
	}
	room := roomName(convID)
	s.hub.Broadcast(room, payload)
	web.JSON(w, 200, payload)
}

func (s *Service) ChatWS(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	convIDStr := r.URL.Query().Get("conversationId")
	if convIDStr == "" {
		http.Error(w, "conversationId required", 400)
		return
	}
	convID, err := strconv.ParseInt(convIDStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}

	conv, err := s.getConversation(r, convID)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if uid != conv.StudentID && uid != conv.MentorID {
		http.Error(w, "forbidden", 403)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	room := roomName(convID)
	s.hub.Join(room, conn)
	defer func() { s.hub.Leave(room, conn); conn.Close() }()

	for {
		// client sends pings; we keep the socket alive
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// --- helpers ---

func (s *Service) getConversation(r *http.Request, id int64) (conversation, error) {
	var c conversation
	err := s.db.QueryRow(r.Context(), `SELECT id, student_id, mentor_id FROM conversations WHERE id=$1`, id).
		Scan(&c.ID, &c.StudentID, &c.MentorID)
	return c, err
}

func (s *Service) isMember(r *http.Request, convID, uid int64) (bool, error) {
	var sid, mid sql.NullInt64
	err := s.db.QueryRow(r.Context(), `SELECT student_id, mentor_id FROM conversations WHERE id=$1`, convID).Scan(&sid, &mid)
	if err != nil {
		return false, err
	}
	if !sid.Valid || !mid.Valid {
		return false, errors.New("invalid conversation")
	}
	return uid == sid.Int64 || uid == mid.Int64, nil
}

func roomName(convID int64) string { return "conv:" + strconv.FormatInt(convID, 10) }
