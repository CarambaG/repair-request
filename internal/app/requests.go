package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (a *App) requests(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	csrf := currentCSRF(r)

	switch r.Method {
	case http.MethodGet:
		items, err := a.listRequests(r.Context(), user)
		if err != nil {
			serverError(w, err)
			return
		}
		a.render(w, r, "requests.html", TemplateData{
			Title:       "Заявки",
			CurrentUser: user,
			CSRF:        csrf,
			Requests:    items,
		})
	case http.MethodPost:
		withBodyLimit(a.cfg.MaxUploadTotalBytes, a.withCSRF(a.createRequest))(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (a *App) requestNew(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user.Role != RoleCustomer {
		http.Error(w, "создавать заявки может только заказчик", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	serviceCatalog, err := a.getServiceCatalog(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}

	a.renderRequestNewForm(w, r, user, serviceCatalog, RequestForm{}, "")
}

func (a *App) createRequest(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user.Role != RoleCustomer {
		http.Error(w, "создавать заявки может только заказчик", http.StatusForbidden)
		return
	}

	serviceCatalog, err := a.getServiceCatalog(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}

	if err := parseRequestForm(r); err != nil {
		a.renderRequestNewForm(w, r, user, serviceCatalog, RequestForm{}, uploadFormErrorMessage(err, a.cfg.MaxUploadTotalBytes))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	title := formValue(r, "title", 160)
	description := formValue(r, "description", 4000)
	address := formValue(r, "address", 255)
	requestServices, serviceQuantities, serviceErr := parseRequestServices(r.PostForm, serviceCatalog)
	form := RequestForm{
		Title:             title,
		Description:       description,
		Address:           address,
		ServiceQuantities: serviceQuantities,
	}

	for field, value := range map[string]string{
		"название": title,
		"описание": description,
		"адрес":    address,
	} {
		if err := validateRequired(value, field); err != nil {
			a.renderRequestNewForm(w, r, user, serviceCatalog, form, err.Error())
			return
		}
	}
	if serviceErr != nil {
		a.renderRequestNewForm(w, r, user, serviceCatalog, form, serviceErr.Error())
		return
	}

	attachments, err := a.storeUploadedFiles(fileHeadersFromRequest(r), false)
	if err != nil {
		a.renderRequestNewForm(w, r, user, serviceCatalog, form, err.Error())
		return
	}
	keepFiles := false
	defer func() {
		if !keepFiles {
			cleanupSavedFiles(attachments)
		}
	}()

	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	var id int64
	err = tx.QueryRow(r.Context(), `
		INSERT INTO repair_requests (title, description, address, customer_id, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, title, description, address, user.ID, string(StatusSentToAccountant)).Scan(&id)
	if err != nil {
		serverError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `
		INSERT INTO request_events (request_id, actor_id, from_status, to_status, comment)
		VALUES ($1, $2, $3, $4, $5)
	`, id, user.ID, "created", string(StatusSentToAccountant), "Заявка создана заказчиком и передана бухгалтеру")
	if err != nil {
		serverError(w, err)
		return
	}
	if err := insertRequestServices(r.Context(), tx, id, requestServices); err != nil {
		serverError(w, err)
		return
	}
	if err := insertAttachments(r.Context(), tx, id, user.ID, attachments); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	keepFiles = true

	http.Redirect(w, r, fmt.Sprintf("/requests/%d", id), http.StatusSeeOther)
}

func (a *App) requestByID(w http.ResponseWriter, r *http.Request) {
	id, tail, ok := parseRequestID(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if tail == "" && r.Method == http.MethodGet {
		a.requestDetail(w, r, id)
		return
	}
	if tail == "action" && r.Method == http.MethodPost {
		a.withCSRF(func(w http.ResponseWriter, r *http.Request) {
			a.requestAction(w, r, id)
		})(w, r)
		return
	}
	if tail == "attachments" && r.Method == http.MethodPost {
		withBodyLimit(a.cfg.MaxUploadTotalBytes, a.withCSRF(func(w http.ResponseWriter, r *http.Request) {
			a.requestAttachments(w, r, id)
		}))(w, r)
		return
	}
	if strings.HasPrefix(tail, "attachments/") && strings.HasSuffix(tail, "/delete") && r.Method == http.MethodPost {
		attachmentIDRaw := strings.TrimSuffix(strings.TrimPrefix(tail, "attachments/"), "/delete")
		a.withCSRF(func(w http.ResponseWriter, r *http.Request) {
			a.requestAttachmentDelete(w, r, id, strings.Trim(attachmentIDRaw, "/"))
		})(w, r)
		return
	}
	if strings.HasPrefix(tail, "attachments/") && r.Method == http.MethodGet {
		a.requestAttachmentDownload(w, r, id, strings.TrimPrefix(tail, "attachments/"))
		return
	}
	http.NotFound(w, r)
}

func (a *App) requestDetail(w http.ResponseWriter, r *http.Request, id int64) {
	user := currentUser(r)
	req, err := a.getRequest(r.Context(), id, user)
	if err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}
	requestServices, err := a.getRequestServices(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return
	}
	events, err := a.getEvents(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return
	}
	attachments, err := a.getAttachments(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return
	}
	a.render(w, r, "request_detail.html", TemplateData{
		Title:           fmt.Sprintf("Заявка №%d", req.ID),
		CurrentUser:     user,
		CSRF:            currentCSRF(r),
		Request:         req,
		RequestServices: requestServices,
		Attachments:     attachments,
		Events:          events,
		Actions:         availableActions(user, req),
	})
}

func (a *App) requestAction(w http.ResponseWriter, r *http.Request, id int64) {
	user := currentUser(r)
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	action := r.PostForm.Get("action")
	comment := formValue(r, "comment", 2000)
	estimateRaw := formValue(r, "estimate", 40)

	err := a.applyAction(r.Context(), id, user, action, comment, estimateRaw)
	if err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/requests/%d", id), http.StatusSeeOther)
}

func (a *App) applyAction(ctx context.Context, id int64, user *User, action, comment, estimateRaw string) error {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var customerID int64
	var currentStatusRaw string
	err = tx.QueryRow(ctx, `
		SELECT customer_id, status
		FROM repair_requests
		WHERE id = $1
		FOR UPDATE
	`, id).Scan(&customerID, &currentStatusRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("заявка не найдена")
		}
		return err
	}
	currentStatus := Status(currentStatusRaw)
	newStatus := currentStatus
	eventComment := comment

	switch action {
	case "accountant_send":
		if user.Role != RoleAccountant || !(currentStatus == StatusSentToAccountant || currentStatus == StatusReturnedToAccountant) {
			return errForbidden
		}
		estimateCents, err := parseRublesToCents(estimateRaw)
		if err != nil {
			return err
		}
		if comment == "" {
			eventComment = "Бухгалтер указал стоимость и передал заявку заказчику"
		}
		newStatus = StatusSentToCustomer
		_, err = tx.Exec(ctx, `
			UPDATE repair_requests
			SET status = $1, estimate_amount_cents = $2, accountant_comment = $3, updated_at = now()
			WHERE id = $4
		`, string(newStatus), estimateCents, comment, id)
		if err != nil {
			return err
		}
	case "customer_approve":
		if user.Role != RoleCustomer || user.ID != customerID || currentStatus != StatusSentToCustomer {
			return errForbidden
		}
		newStatus = StatusApprovedForWorkers
		if comment == "" {
			eventComment = "Заказчик согласовал заявку и передал ее рабочим"
		}
		_, err = tx.Exec(ctx, `
			UPDATE repair_requests
			SET status = $1, customer_comment = $2, updated_at = now()
			WHERE id = $3
		`, string(newStatus), comment, id)
		if err != nil {
			return err
		}
	case "customer_return":
		if user.Role != RoleCustomer || user.ID != customerID || currentStatus != StatusSentToCustomer {
			return errForbidden
		}
		newStatus = StatusReturnedToAccountant
		if comment == "" {
			return fmt.Errorf("укажите причину возврата бухгалтеру")
		}
		_, err = tx.Exec(ctx, `
			UPDATE repair_requests
			SET status = $1, customer_comment = $2, updated_at = now()
			WHERE id = $3
		`, string(newStatus), comment, id)
		if err != nil {
			return err
		}
	case "worker_start":
		if user.Role != RoleWorker || currentStatus != StatusApprovedForWorkers {
			return errForbidden
		}
		newStatus = StatusInProgress
		if comment == "" {
			eventComment = "Рабочий взял заявку в работу"
		}
		_, err = tx.Exec(ctx, `
			UPDATE repair_requests
			SET status = $1, worker_comment = $2, updated_at = now()
			WHERE id = $3
		`, string(newStatus), comment, id)
		if err != nil {
			return err
		}
	case "worker_complete":
		if user.Role != RoleWorker || currentStatus != StatusInProgress {
			return errForbidden
		}
		newStatus = StatusCompleted
		if comment == "" {
			eventComment = "Работы по заявке завершены"
		}
		_, err = tx.Exec(ctx, `
			UPDATE repair_requests
			SET status = $1, worker_comment = $2, updated_at = now()
			WHERE id = $3
		`, string(newStatus), comment, id)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("неизвестное действие")
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO request_events (request_id, actor_id, from_status, to_status, comment)
		VALUES ($1, $2, $3, $4, $5)
	`, id, user.ID, string(currentStatus), string(newStatus), eventComment)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *App) renderRequestNewForm(w http.ResponseWriter, r *http.Request, user *User, serviceCatalog []ServiceOption, form RequestForm, errorText string) {
	a.render(w, r, "request_new.html", TemplateData{
		Title:          "Новая заявка",
		CurrentUser:    user,
		CSRF:           currentCSRF(r),
		Error:          errorText,
		Form:           form,
		ServiceCatalog: serviceCatalog,
	})
}

func parseRublesToCents(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, " ", "")
	raw = strings.ReplaceAll(raw, ",", ".")
	if raw == "" {
		return 0, fmt.Errorf("укажите стоимость работ")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("стоимость указана некорректно")
	}
	rubles, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || rubles < 0 {
		return 0, fmt.Errorf("стоимость указана некорректно")
	}
	kopecks := int64(0)
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
		if len(fraction) > 2 {
			fraction = fraction[:2]
		}
		kopecks, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil || kopecks < 0 {
			return 0, fmt.Errorf("стоимость указана некорректно")
		}
	}
	return rubles*100 + kopecks, nil
}
