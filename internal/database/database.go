package database

import (
	"context"
	"embed"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

//go:embed migrations
var migrationFiles embed.FS

func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if _, err := pool.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("execute migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func SeedUsers(ctx context.Context, pool *pgxpool.Pool, bcryptCost int) error {
	users := []struct {
		Email    string
		Name     string
		Role     string
		Password string
	}{
		{Email: "customer@example.com", Name: "Иван Заказчик", Role: "customer", Password: "password123"},
		{Email: "accountant@example.com", Name: "Ольга Бухгалтер", Role: "accountant", Password: "password123"},
		{Email: "worker@example.com", Name: "Сергей Рабочий", Role: "worker", Password: "password123"},
	}

	for _, user := range users {
		hash, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcryptCost)
		if err != nil {
			return fmt.Errorf("hash seed password: %w", err)
		}
		_, err = pool.Exec(ctx, `
			INSERT INTO users (email, password_hash, name, role)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (email) DO NOTHING
		`, user.Email, string(hash), user.Name, user.Role)
		if err != nil {
			return fmt.Errorf("insert seed user %s: %w", user.Email, err)
		}
	}
	return nil
}
