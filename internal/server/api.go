package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"upskill/internal/auth"
	"upskill/internal/chat"
	"upskill/internal/config"
	"upskill/internal/mentorship"
	"upskill/internal/planner"
	"upskill/internal/roles"
)

func newAPI(cfg config.Config, pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()

	authSvc := auth.NewService(cfg, pool)
	r.Post("/auth/register", authSvc.Register)
	r.Post("/auth/login", authSvc.Login)

	r.Group(func(r chi.Router) {
		r.Use(authSvc.JWTMiddleware)

		r.Get("/user/me", authSvc.Me)

		roleSvc := roles.NewService(pool)
		r.Post("/roles", roleSvc.Assign) // <- было Add
		r.Get("/roles/me", roleSvc.Me)

		ms := mentorship.NewService(pool)
		r.Post("/mentorship/requests", ms.RequestCreate)    // student
		r.Get("/mentorship/requests", ms.MyRequests)        // student outgoing
		r.Get("/mentor/requests", ms.MentorRequests)        // mentor incoming
		r.Post("/mentor/requests/{id}/approve", ms.Approve) // mentor
		r.Post("/mentor/requests/{id}/decline", ms.Decline) // mentor
		r.Get("/mentor/mentees", ms.ListMentees)            // mentor
		r.Get("/student/mentors", ms.ListMentors)           // student

		ch := chat.NewService(pool, authSvc)
		r.Get("/chat/global/messages", ch.GlobalHistory)
		r.Post("/chat/global/messages", ch.GlobalPost)
		r.Get("/chat/conversations", ch.ListConversations)
		r.Post("/chat/conversations", ch.EnsureConversation)
		r.Get("/chat/conversations/{id}/messages", ch.History)
		r.Post("/chat/conversations/{id}/messages", ch.PostMessage)
		r.Get("/ws/chat/global", ch.GlobalWS)
		r.Get("/ws/chat", ch.ChatWS)

		pl := planner.NewService(cfg, pool)
		r.Post("/plans/generate", pl.Generate)
		r.Get("/plans", pl.List)
		r.Get("/plans/{id}", pl.Get)
		r.Post("/plans/{id}/tasks/{taskId}/complete", pl.CompleteTask)

	})

	return r
}
