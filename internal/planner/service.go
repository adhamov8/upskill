package planner

import (
	"time"
	"net/http"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"upskill/internal/auth"
	"upskill/internal/config"
	"upskill/internal/web"
)

type Service struct {
	cfg  config.Config
	db   *pgxpool.Pool
}

func NewService(cfg config.Config, db *pgxpool.Pool) *Service {
	return &Service{cfg: cfg, db: db}
}

func (s *Service) Generate(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	var in struct {
		Topic        string `json:"topic"`
		Level        string `json:"level"`
		HoursPerWeek int    `json:"hoursPerWeek"`
		StartDate    string `json:"startDate"`
		Weeks        int    `json:"weeks"`
	}
	if err := web.DecodeJSON(r, &in); err != nil || in.Topic == "" {
		http.Error(w, "bad input", 400); return
	}
	if in.HoursPerWeek <= 0 { in.HoursPerWeek = 4 }
	if in.Weeks <= 0 { in.Weeks = 4 }
	if in.Level == "" { in.Level = "beginner" }

	start, err := time.Parse("2006-01-02", in.StartDate)
	if err != nil { http.Error(w, "bad startDate", 400); return }

	var planID int64
	err = s.db.QueryRow(r.Context(), `
		INSERT INTO plans(user_id, topic, level, hours_per_week, start_date, weeks)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING id
	`, uid, in.Topic, in.Level, in.HoursPerWeek, start, in.Weeks).Scan(&planID)
	if err != nil { http.Error(w, err.Error(), 500); return }

	slots := buildSlots(start, in.Weeks, in.HoursPerWeek)
	order := 0
	for i, t := range slots {
		order++
		title := fmt.Sprintf("%s — Session %d", in.Topic, i+1)
		desc := genDescription(in.Level, i+1)
		_, err := s.db.Exec(r.Context(), `
			INSERT INTO plan_tasks(plan_id, title, description, start_time, end_time, status, order_no)
			VALUES($1,$2,$3,$4,$5,'pending',$6)
		`, planID, title, desc, t.Start, t.End, order)
		if err != nil { http.Error(w, err.Error(), 500); return }
	}

	web.JSON(w, 201, map[string]any{"planId": planID})
}

func (s *Service) List(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT id, topic, level, hours_per_week, start_date, weeks, created_at
		FROM plans WHERE user_id=$1 ORDER BY created_at DESC
	`, uid)
	if err != nil { http.Error(w, err.Error(), 500); return }
	defer rows.Close()

	type P struct {
		ID int64 `json:"id"`
		Topic string `json:"topic"`
		Level string `json:"level"`
		HoursPerWeek int `json:"hoursPerWeek"`
		StartDate string `json:"startDate"`
		Weeks int `json:"weeks"`
		CreatedAt time.Time `json:"createdAt"`
	}
	var items []P
	for rows.Next() {
		var it P
		var start time.Time
		if err := rows.Scan(&it.ID, &it.Topic, &it.Level, &it.HoursPerWeek, &start, &it.Weeks, &it.CreatedAt); err == nil {
			it.StartDate = start.Format("2006-01-02")
			items = append(items, it)
		}
	}
	web.JSON(w, 200, map[string]any{"items": items})
}

func (s *Service) Get(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	pid, err := web.ParamInt64(r, "id")
	if err != nil { http.Error(w, "bad id", 400); return }

	var owner int64
	var topic, level string
	var hpw, weeks int
	var start time.Time
	err = s.db.QueryRow(r.Context(), `
		SELECT user_id, topic, level, hours_per_week, start_date, weeks
		FROM plans WHERE id=$1
	`, pid).Scan(&owner, &topic, &level, &hpw, &start, &weeks)
	if err != nil { http.Error(w, "not found", 404); return }
	if owner != uid { http.Error(w, "forbidden", 403); return }

	rows, err := s.db.Query(r.Context(), `
		SELECT id, title, description, start_time, end_time, status, order_no
		FROM plan_tasks WHERE plan_id=$1 ORDER BY start_time ASC
	`, pid)
	if err != nil { http.Error(w, err.Error(), 500); return }
	defer rows.Close()

	type T struct {
		ID int64 `json:"id"`
		Title string `json:"title"`
		Description string `json:"description"`
		Start string `json:"start"`
		End   string `json:"end"`
		Status string `json:"status"`
		Order int `json:"order"`
	}
	var tasks []T
	for rows.Next() {
		var t T
		var st, en time.Time
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &st, &en, &t.Status, &t.Order); err == nil {
			t.Start = st.UTC().Format(time.RFC3339)
			t.End = en.UTC().Format(time.RFC3339)
			tasks = append(tasks, t)
		}
	}
	web.JSON(w, 200, map[string]any{
		"plan": map[string]any{
			"id": pid, "topic": topic, "level": level, "hoursPerWeek": hpw,
			"startDate": start.Format("2006-01-02"), "weeks": weeks,
		},
		"tasks": tasks,
	})
}

func (s *Service) CompleteTask(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	pid, err := web.ParamInt64(r, "id")
	if err != nil { http.Error(w, "bad id", 400); return }
	tid, err := web.ParamInt64(r, "taskId")
	if err != nil { http.Error(w, "bad taskId", 400); return }

	var owner int64
	if err := s.db.QueryRow(r.Context(), `SELECT user_id FROM plans WHERE id=$1`, pid).Scan(&owner); err != nil {
		http.Error(w, "not found", 404); return
	}
	if owner != uid { http.Error(w, "forbidden", 403); return }

	ct, err := s.db.Exec(r.Context(), `UPDATE plan_tasks SET status='completed' WHERE id=$1 AND plan_id=$2`, tid, pid)
	if err != nil { http.Error(w, err.Error(), 500); return }
	if ct.RowsAffected() == 0 { http.Error(w, "not found", 404); return }
	web.JSON(w, 200, map[string]any{"ok": true})
}

type slot struct {
	Start time.Time
	End   time.Time
}

func buildSlots(start time.Time, weeks, hoursPerWeek int) []slot {
	d := start
	if d.Hour() != 19 {
		d = time.Date(d.Year(), d.Month(), d.Day(), 19, 0, 0, 0, time.UTC)
	}
	var res []slot
	for w := 0; w < weeks; w++ {
		cnt := 0
		day := d.AddDate(0, 0, w*7)
		for i := 0; cnt < hoursPerWeek && i < 7; i++ {
			st := day.AddDate(0, 0, i)
			res = append(res, slot{Start: st, End: st.Add(time.Hour)})
			cnt++
		}
	}
	return res
}

func genDescription(level string, idx int) string {
	switch level {
	case "advanced":
		return fmt.Sprintf("Deep dive session %d: projects, mastery, and review.", idx)
	case "intermediate":
		return fmt.Sprintf("Practice session %d: skills consolidation and tasks.", idx)
	default:
		return fmt.Sprintf("Foundations session %d: basics and exercises.", idx)
	}
}
