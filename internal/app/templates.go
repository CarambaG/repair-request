package app

import (
	"fmt"
	"html/template"
	"path/filepath"
	"time"
)

type TemplateData struct {
	Title           string
	CurrentUser     *User
	CSRF            string
	Error           string
	Notice          string
	Next            string
	Form            RequestForm
	ServiceCatalog  []ServiceOption
	Requests        []RepairRequest
	Request         *RepairRequest
	RequestServices []RequestServiceItem
	Attachments     []RequestAttachment
	Events          []RequestEvent
	Actions         map[string]bool
}

func loadTemplates() (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"roleLabel":       roleLabel,
		"statusLabel":     statusLabel,
		"formatTime":      formatTime,
		"formatMoney":     formatMoney,
		"formatBytes":     formatBytes,
		"serviceQtyValue": serviceQtyValue,
	}

	pages := []string{
		"login.html",
		"register.html",
		"requests.html",
		"request_new.html",
		"request_detail.html",
	}

	templates := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		parsed, err := template.New("base.html").Funcs(funcs).ParseFiles(
			filepath.Join("web", "templates", "base.html"),
			filepath.Join("web", "templates", page),
		)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", page, err)
		}
		templates[page] = parsed
	}
	return templates, nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("02.01.2006 15:04")
}

func formatMoney(cents *int64) string {
	if cents == nil {
		return "не указана"
	}
	rubles := *cents / 100
	kopecks := *cents % 100
	return fmt.Sprintf("%d,%02d ₽", rubles, kopecks)
}

func formatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	value := float64(size)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PiB", value/unit)
}
