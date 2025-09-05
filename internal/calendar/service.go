package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"upskill/internal/auth"
	"upskill/internal/config"
	"upskill/internal/web"
)

type Service struct {
	cfg   config.Config
	db    *pgxpool.Pool
	auth  *auth.Service
	oauth *oauth2.Config
}

func NewService(cfg config.Config, db *pgxpool.Pool, authSvc *auth.Service) *Service {
	return &Service{
		cfg:  cfg,
		db:   db,
		auth: authSvc,
		oauth: &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.CalendarRedirectURL,
			Scopes:       []string{calendar.CalendarEventsScope},
			Endpoint:     google.Endpoint,
		},
	}
}

func (s *Service) Connect(w http.ResponseWriter, r *http.Request) {
	url := s.oauth.AuthCodeURL("state-cal", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Service) Callback(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", 400); return
	}
	tok, err := s.oauth.Exchange(r.Context(), code)
	if err != nil { http.Error(w, "exchange: "+err.Error(), 400); return }
	if tok.RefreshToken == "" {
		http.Error(w, "no refresh_token (try logout/consent)", 400); return
	}
	_, err = s.db.Exec(r.Context(), `
		INSERT INTO google_calendar_tokens(user_id, refresh_token, scope)
		VALUES($1,$2,'calendar.events')
		ON CONFLICT (user_id) DO UPDATE SET refresh_token=EXCLUDED.refresh_token, updated_at=now()
	`, uid, tok.RefreshToken)
	if err != nil { http.Error(w, err.Error(), 500); return }
	web.JSON(w, 200, map[string]any{"ok": true})
}

func (s *Service) Sync(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	var in struct{ PlanID int64 `json:"planId"` }
	if err := web.DecodeJSON(r, &in); err != nil || in.PlanID <= 0 {
		http.Error(w, "bad input", 400); return
	}

	var owner int64
	if err := s.db.QueryRow(r.Context(), `SELECT user_id FROM plans WHERE id=$1`, in.PlanID).Scan(&owner); err != nil {
		http.Error(w, "not found", 404); return
	}
	if owner != uid { http.Error(w, "forbidden", 403); return }

	rows, err := s.db.Query(r.Context(), `
		SELECT id, title, COALESCE(description,''), start_time, end_time
		FROM plan_tasks WHERE plan_id=$1 ORDER BY start_time ASC
	`, in.PlanID)
	if err != nil { http.Error(w, err.Error(), 500); return }
	defer rows.Close()

	type task struct {
		ID int64
		Title, Desc string
		Start, End time.Time
	}
	var tasks []task
	for rows.Next() {
		var t task
		if err := rows.Scan(&t.ID, &t.Title, &t.Desc, &t.Start, &t.End); err == nil {
			tasks = append(tasks, t)
		}
	}

	if !s.cfg.CalendarEnabled {
		web.JSON(w, 200, map[string]any{
			"synced": false, "message": "Google Calendar disabled (GOOGLE_CALENDAR_ENABLED=0). Use /api/calendar/ics?planId=... to download ICS.",
			"events": len(tasks),
		})
		return
	}

	var refresh string
	err = s.db.QueryRow(r.Context(), `SELECT refresh_token FROM google_calendar_tokens WHERE user_id=$1`, uid).Scan(&refresh)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "no calendar linked", 400); return
		}
		http.Error(w, err.Error(), 500); return
	}

	srv, err := s.calendarClient(r.Context(), refresh)
	if err != nil { http.Error(w, "calendar client: "+err.Error(), 500); return }

	calID, err := ensureCalendar(r.Context(), srv, "UpSkill")
	if err != nil { http.Error(w, "ensure calendar: "+err.Error(), 500); return }

	for _, t := range tasks {
		id := fmt.Sprintf("upskill-%d-%d", in.PlanID, t.ID)
		ev := &calendar.Event{
			Id:          sanitizeID(strings.ToLower(id)),
			Summary:     t.Title,
			Description: t.Desc,
			Start:       &calendar.EventDateTime{DateTime: t.Start.Format(time.RFC3339)},
			End:         &calendar.EventDateTime{DateTime: t.End.Format(time.RFC3339)},
		}
		if _, err := srv.Events.Update(calID, ev.Id, ev).Do(); err != nil {
			_, err = srv.Events.Insert(calID, ev).Do()
			if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				http.Error(w, "event upsert: "+err.Error(), 500); return
			}
		}
	}
	web.JSON(w, 200, map[string]any{"synced": true, "events": len(tasks)})
}

func (s *Service) ICS(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	planIDStr := r.URL.Query().Get("planId")
	if planIDStr == "" {
		http.Error(w, "planId required", 400); return
	}
	var owner int64
	if err := s.db.QueryRow(r.Context(), `SELECT user_id FROM plans WHERE id=$1`, planIDStr).Scan(&owner); err != nil {
		http.Error(w, "not found", 404); return
	}
	if owner != uid { http.Error(w, "forbidden", 403); return }

	rows, err := s.db.Query(r.Context(), `
		SELECT id, title, COALESCE(description,''), start_time, end_time
		FROM plan_tasks WHERE plan_id=$1 ORDER BY start_time ASC
	`, planIDStr)
	if err != nil { http.Error(w, err.Error(), 500); return }
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Title, &it.Desc, &it.Start, &it.End); err == nil {
			items = append(items, it)
		}
	}
	ics := BuildICS(items)
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=upskill.ics")
	_, _ = w.Write(ics)
}

func (s *Service) calendarClient(ctx context.Context, refresh string) (*calendar.Service, error) {
	src := oauth2.ReuseTokenSource(&oauth2.Token{RefreshToken: refresh}, s.oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refresh}))
	httpClient := oauth2.NewClient(ctx, src)
	return calendar.NewService(ctx, option.WithHTTPClient(httpClient))
}

func ensureCalendar(ctx context.Context, srv *calendar.Service, summary string) (string, error) {
	lst, err := srv.CalendarList.List().Do()
	if err == nil {
		for _, it := range lst.Items {
			if it.Summary == summary {
				return it.Id, nil
			}
		}
	}
	cal, err := srv.Calendars.Insert(&calendar.Calendar{Summary: summary, TimeZone: "UTC"}).Do()
	if err != nil { return "", err }
	return cal.Id, nil
}

func sanitizeID(s string) string {
	ok := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			ok = append(ok, r)
		} else {
			ok = append(ok, '-')
		}
	}
	return strings.Trim(strings.ReplaceAll(string(ok), "--", "-"), "-")
}
