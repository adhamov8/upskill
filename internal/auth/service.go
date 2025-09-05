package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"upskill/internal/config"
	"upskill/internal/web"
)

type Service struct {
	cfg   config.Config
	db    *pgxpool.Pool
	priv  *rsa.PrivateKey
	pub   *rsa.PublicKey
	oauth *oauth2.Config
	oidcV *oidc.IDTokenVerifier
}

func NewService(cfg config.Config, db *pgxpool.Pool) *Service {
	s := &Service{cfg: cfg, db: db}
	s.initKeys()
	s.oauth = &oauth2.Config{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		RedirectURL:  cfg.GoogleRedirectURL,
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}
	if cfg.GoogleClientID != "" {
		if provider, err := oidc.NewProvider(context.Background(), "https://accounts.google.com"); err == nil {
			s.oidcV = provider.Verifier(&oidc.Config{ClientID: cfg.GoogleClientID})
		}
	}
	return s
}

func (s *Service) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/register", s.Register)
	r.Post("/login", s.Login)
	r.Get("/google/login", s.GoogleLogin)
	r.Get("/google/callback", s.GoogleCallback)
	return r
}

func (s *Service) initKeys() {
	if strings.TrimSpace(s.cfg.JWTPrivatePEM) != "" && strings.TrimSpace(s.cfg.JWTPublicPEM) != "" {
		if block, _ := pem.Decode([]byte(s.cfg.JWTPrivatePEM)); block != nil {
			if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
				key.Precompute()
				s.priv = key
				s.pub = &key.PublicKey
				return
			}
		}
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	s.priv = key
	s.pub = &key.PublicKey
}

type user struct {
	ID        int64
	Email     string
	PassHash  sql.NullString
	FirstName sql.NullString
	LastName  sql.NullString
	AvatarURL sql.NullString
}

func (s *Service) Register(w http.ResponseWriter, r *http.Request) {
	type inT struct {
		Email     string `json:"email"`
		Password  string `json:"password"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	}
	var in inT
	if err := web.DecodeJSON(r, &in); err != nil || in.Email == "" || in.Password == "" {
		http.Error(w, "bad input", http.StatusBadRequest)
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	var id int64
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO users(email,password_hash,first_name,last_name)
		VALUES($1,$2,$3,$4)
		RETURNING id
	`, strings.ToLower(in.Email), string(hash), nullIfEmpty(in.FirstName), nullIfEmpty(in.LastName)).Scan(&id); err != nil {
		http.Error(w, "email exists?", http.StatusConflict)
		return
	}
	tok, _ := s.issueJWT(id)
	web.JSON(w, http.StatusOK, map[string]any{
		"accessToken": tok,
		"user":        map[string]any{"id": id, "email": strings.ToLower(in.Email), "firstName": in.FirstName, "lastName": in.LastName},
	})
}

func (s *Service) Login(w http.ResponseWriter, r *http.Request) {
	type inT struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	var in inT
	if err := web.DecodeJSON(r, &in); err != nil {
		http.Error(w, "bad input", http.StatusBadRequest)
		return
	}
	var id int64
	var hash sql.NullString
	if err := s.db.QueryRow(r.Context(), `SELECT id,password_hash FROM users WHERE email=$1`, strings.ToLower(in.Email)).Scan(&id, &hash); err != nil || !hash.Valid {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash.String), []byte(in.Password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	tok, _ := s.issueJWT(id)
	web.JSON(w, http.StatusOK, map[string]any{"accessToken": tok, "user": map[string]any{"id": id, "email": strings.ToLower(in.Email)}})
}

func (s *Service) Me(w http.ResponseWriter, r *http.Request) {
	uid := UserID(r)
	var email string
	var first, last sql.NullString
	if err := s.db.QueryRow(r.Context(), `SELECT email, first_name, last_name FROM users WHERE id=$1`, uid).Scan(&email, &first, &last); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT role FROM user_roles WHERE user_id=$1`, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	roles := []string{}
	for rows.Next() {
		var role string
		_ = rows.Scan(&role)
		roles = append(roles, role)
	}
	web.JSON(w, http.StatusOK, map[string]any{
		"user":  map[string]any{"id": uid, "email": email, "firstName": first.String, "lastName": last.String},
		"roles": roles,
	})
}

func (s *Service) issueJWT(uid int64) (string, error) {
	claims := jwt.MapClaims{
		"sub": uid,
		"exp": time.Now().Add(72 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(s.priv)
}

func (s *Service) JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		raw := strings.TrimPrefix(h, "Bearer ")
		tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
			if t.Method != jwt.SigningMethodRS256 {
				return nil, errors.New("alg")
			}
			return s.pub, nil
		})
		if err != nil || !tok.Valid {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		cl, _ := tok.Claims.(jwt.MapClaims)
		uidF := cl["sub"]
		var uid int64
		switch v := uidF.(type) {
		case float64:
			uid = int64(v)
		case int64:
			uid = v
		default:
			http.Error(w, "bad sub", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUserID, uid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	url := s.oauth.AuthCodeURL("state-upskill", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Service) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" || s.oidcV == nil {
		http.Error(w, "not configured", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	tok, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		http.Error(w, "oauth exchange: "+err.Error(), http.StatusBadRequest)
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		http.Error(w, "no id_token", http.StatusBadRequest)
		return
	}
	idTok, err := s.oidcV.Verify(ctx, rawID)
	if err != nil {
		http.Error(w, "verify id_token: "+err.Error(), http.StatusBadRequest)
		return
	}
	var claims struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := idTok.Claims(&claims); err != nil {
		http.Error(w, "claims: "+err.Error(), http.StatusBadRequest)
		return
	}
	uid, err := s.upsertUserGoogle(ctx, claims.Sub, strings.ToLower(claims.Email), claims.Name, claims.Picture)
	if err != nil {
		http.Error(w, "upsert: "+err.Error(), http.StatusInternalServerError)
		return
	}
	j, _ := s.issueJWT(uid)
	web.JSON(w, http.StatusOK, map[string]any{"accessToken": j, "user": map[string]any{"id": uid, "email": claims.Email, "name": claims.Name}})
}

func (s *Service) upsertUserGoogle(ctx context.Context, sub, email, name, avatar string) (int64, error) {
	var uid int64
	if err := s.db.QueryRow(ctx, `SELECT user_id FROM user_providers WHERE provider='google' AND subject=$1`, sub).Scan(&uid); err == nil {
		return uid, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if err := s.db.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&uid); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		first, last := splitName(name)
		if err := s.db.QueryRow(ctx, `
			INSERT INTO users(email, first_name, last_name, avatar_url)
			VALUES($1,$2,$3,$4) RETURNING id
		`, email, nullIfEmpty(first), nullIfEmpty(last), nullIfEmpty(avatar)).Scan(&uid); err != nil {
			return 0, err
		}
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO user_providers(user_id, provider, subject, email)
		VALUES($1,'google',$2,$3)
		ON CONFLICT (provider, subject) DO NOTHING
	`, uid, sub, email)
	return uid, err
}

type ctxKey int

const ctxKeyUserID ctxKey = 1

func UserID(r *http.Request) int64 {
	v := r.Context().Value(ctxKeyUserID)
	if v == nil {
		return 0
	}
	return v.(int64)
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func splitName(full string) (string, string) {
	full = strings.TrimSpace(full)
	if full == "" {
		return "", ""
	}
	parts := strings.Fields(full)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}
