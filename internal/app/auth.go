package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	switch r.Method {
	case http.MethodGet:
		a.render(w, r, "login.html", TemplateData{Title: "Вход", Next: r.URL.Query().Get("next")})
	case http.MethodPost:
		a.loginPost(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (a *App) loginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}

	key := clientIP(r)
	if !a.loginLimiter.allow(key) {
		a.render(w, r, "login.html", TemplateData{Title: "Вход", Next: r.URL.Query().Get("next"), Error: "Слишком много попыток входа. Повторите позже."})
		return
	}

	email := strings.ToLower(formValue(r, "email", 255))
	password := r.PostForm.Get("password")
	user, passwordHash, err := a.findUserByEmail(r.Context(), email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		a.loginLimiter.fail(key)
		a.render(w, r, "login.html", TemplateData{Title: "Вход", Next: r.URL.Query().Get("next"), Error: "Неверная почта или пароль."})
		return
	}

	token, csrf, expiresAt, err := a.createSession(r.Context(), user.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	_ = csrf
	a.loginLimiter.success(key)
	http.SetCookie(w, loginCookie(token, expiresAt, a.cfg.CookieSecure))

	next := r.URL.Query().Get("next")
	if !isSafeRedirect(next) {
		next = "/requests"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	switch r.Method {
	case http.MethodGet:
		a.render(w, r, "register.html", TemplateData{Title: "Регистрация заказчика"})
	case http.MethodPost:
		a.registerPost(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (a *App) registerPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}

	name := formValue(r, "name", 120)
	email := strings.ToLower(formValue(r, "email", 255))
	password := r.PostForm.Get("password")

	if err := validateRequired(name, "имя"); err != nil {
		a.render(w, r, "register.html", TemplateData{Title: "Регистрация заказчика", Error: err.Error()})
		return
	}
	if !strings.Contains(email, "@") || len(email) < 5 {
		a.render(w, r, "register.html", TemplateData{Title: "Регистрация заказчика", Error: "Укажите корректную почту."})
		return
	}
	if len(password) < 8 {
		a.render(w, r, "register.html", TemplateData{Title: "Регистрация заказчика", Error: "Пароль должен быть не короче 8 символов."})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), a.cfg.BcryptCost)
	if err != nil {
		serverError(w, err)
		return
	}
	_, err = a.db.Exec(r.Context(), `
		INSERT INTO users (email, password_hash, name, role)
		VALUES ($1, $2, $3, $4)
	`, email, string(hash), name, string(RoleCustomer))
	if err != nil {
		a.render(w, r, "register.html", TemplateData{Title: "Регистрация заказчика", Error: "Пользователь с такой почтой уже есть или данные некорректны."})
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		_, _ = a.db.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash = $1`, hashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) findUserByEmail(ctx context.Context, email string) (*User, string, error) {
	var user User
	var role string
	var passwordHash string
	err := a.db.QueryRow(ctx, `
		SELECT id, email, name, role, password_hash
		FROM users
		WHERE email = $1
	`, email).Scan(&user.ID, &user.Email, &user.Name, &role, &passwordHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", fmt.Errorf("user not found")
		}
		return nil, "", err
	}
	user.Role = Role(role)
	return &user, passwordHash, nil
}

func (a *App) createSession(ctx context.Context, userID int64) (token string, csrf string, expiresAt time.Time, err error) {
	token, err = randomToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	csrf, err = randomToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	expiresAt = time.Now().Add(a.cfg.SessionTTL)
	_, err = a.db.Exec(ctx, `
		INSERT INTO sessions (token_hash, csrf_token, user_id, expires_at)
		VALUES ($1, $2, $3, $4)
	`, hashToken(token), csrf, userID, expiresAt)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return token, csrf, expiresAt, nil
}

func (a *App) currentSession(r *http.Request) (*User, *Session, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, nil, fmt.Errorf("session cookie not found")
	}

	var user User
	var session Session
	var role string
	err = a.db.QueryRow(r.Context(), `
		SELECT s.id, s.user_id, s.csrf_token, s.expires_at, u.id, u.email, u.name, u.role
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()
	`, hashToken(cookie.Value)).Scan(
		&session.ID,
		&session.UserID,
		&session.CSRFToken,
		&session.ExpiresAt,
		&user.ID,
		&user.Email,
		&user.Name,
		&role,
	)
	if err != nil {
		return nil, nil, err
	}
	user.Role = Role(role)
	_, _ = a.db.Exec(r.Context(), `UPDATE sessions SET last_seen_at = now() WHERE id = $1`, session.ID)
	return &user, &session, nil
}
