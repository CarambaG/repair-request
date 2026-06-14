package app

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"repair-request/internal/config"
)

const multipartMemoryBytes int64 = 8 << 20

type App struct {
	cfg          config.Config
	db           *pgxpool.Pool
	templates    map[string]*template.Template
	loginLimiter *loginLimiter
}

func New(cfg config.Config, db *pgxpool.Pool) (*App, error) {
	templates, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create upload dir: %w", err)
	}
	return &App{
		cfg:          cfg,
		db:           db,
		templates:    templates,
		loginLimiter: newLoginLimiter(),
	}, nil
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.HandleFunc("/login", a.login)
	mux.HandleFunc("/register", a.register)
	mux.HandleFunc("/logout", a.withAuth(a.withCSRF(a.logout)))
	mux.HandleFunc("/requests", a.withAuth(a.requests))
	mux.HandleFunc("/requests/new", a.withAuth(a.requestNew))
	mux.HandleFunc("/requests/", a.withAuth(a.requestByID))
	mux.HandleFunc("/", a.home)
	return secureHeaders(mux)
}

func (a *App) render(w http.ResponseWriter, r *http.Request, name string, data TemplateData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates[name].ExecuteTemplate(w, "base", data); err != nil {
		slog.Error("render template", "template", name, "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if _, _, err := a.currentSession(r); err == nil {
		http.Redirect(w, r, "/requests", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) withAuth(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, session, err := a.currentSession(r)
		if err != nil {
			redirect := r.URL.RequestURI()
			if !isSafeRedirect(redirect) {
				redirect = "/requests"
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(redirect), http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		ctx = context.WithValue(ctx, sessionContextKey, session)
		next(w, r.WithContext(ctx))
	}
}

func (a *App) withCSRF(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next(w, r)
			return
		}
		session, ok := r.Context().Value(sessionContextKey).(*Session)
		if !ok || session == nil {
			http.Error(w, "csrf session not found", http.StatusForbidden)
			return
		}
		if err := parseRequestForm(r); err != nil {
			handleFormParseError(w, err)
			return
		}
		if r.PostForm.Get("_csrf") != session.CSRFToken {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func currentUser(r *http.Request) *User {
	user, _ := r.Context().Value(userContextKey).(*User)
	return user
}

func currentCSRF(r *http.Request) string {
	session, _ := r.Context().Value(sessionContextKey).(*Session)
	if session == nil {
		return ""
	}
	return session.CSRFToken
}

func parseRequestID(path string) (int64, string, bool) {
	trimmed := strings.TrimPrefix(path, "/requests/")
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return 0, "", false
	}
	parts := strings.Split(trimmed, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	if len(parts) == 1 {
		return id, "", true
	}
	return id, strings.Join(parts[1:], "/"), true
}

func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func badRequest(w http.ResponseWriter, message string) {
	http.Error(w, message, http.StatusBadRequest)
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("server error", "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func withBodyLimit(maxBytes int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if maxBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next(w, r)
	}
}

func formValue(r *http.Request, key string, maxLen int) string {
	value := strings.TrimSpace(r.PostForm.Get(key))
	value = strings.ReplaceAll(value, "\x00", "")
	if len([]rune(value)) > maxLen {
		value = string([]rune(value)[:maxLen])
	}
	return value
}

func validateRequired(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("поле «%s» обязательно", field)
	}
	return nil
}

func errorStatus(err error) int {
	if errors.Is(err, errForbidden) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

func parseRequestForm(r *http.Request) error {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return r.ParseMultipartForm(multipartMemoryBytes)
	}
	return r.ParseForm()
}

func handleFormParseError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid form", http.StatusBadRequest)
}

func (a *App) CleanupExpiredSessions(ctx context.Context) {
	_, _ = a.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
}

func noCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func loginCookie(token string, expiresAt time.Time, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}
