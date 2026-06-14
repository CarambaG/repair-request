package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (a *App) listRequests(ctx context.Context, user *User) ([]RepairRequest, error) {
	query := `
		SELECT r.id, r.title, r.description, r.address, r.customer_id, u.name, u.email,
		       r.status, r.estimate_amount_cents, r.accountant_comment, r.customer_comment, r.worker_comment,
		       r.created_at, r.updated_at
		FROM repair_requests r
		JOIN users u ON u.id = r.customer_id
	`
	var args []any

	switch user.Role {
	case RoleCustomer:
		query += ` WHERE r.customer_id = $1`
		args = append(args, user.ID)
	case RoleAccountant:
		query += ` WHERE r.status IN ('sent_to_accountant', 'sent_to_customer', 'returned_to_accountant', 'approved_for_workers', 'in_progress', 'completed')`
	case RoleWorker:
		query += ` WHERE r.status IN ('approved_for_workers', 'in_progress', 'completed')`
	default:
		return nil, errForbidden
	}
	query += ` ORDER BY r.updated_at DESC, r.id DESC`

	rows, err := a.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RepairRequest
	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, req)
	}
	return result, rows.Err()
}

func (a *App) getRequest(ctx context.Context, id int64, user *User) (*RepairRequest, error) {
	row := a.db.QueryRow(ctx, `
		SELECT r.id, r.title, r.description, r.address, r.customer_id, u.name, u.email,
		       r.status, r.estimate_amount_cents, r.accountant_comment, r.customer_comment, r.worker_comment,
		       r.created_at, r.updated_at
		FROM repair_requests r
		JOIN users u ON u.id = r.customer_id
		WHERE r.id = $1
	`, id)
	req, err := scanRequest(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("заявка не найдена")
		}
		return nil, err
	}
	if !canViewRequest(user, req) {
		return nil, errForbidden
	}
	return &req, nil
}

func (a *App) getEvents(ctx context.Context, requestID int64) ([]RequestEvent, error) {
	rows, err := a.db.Query(ctx, `
		SELECT e.id, e.request_id, u.name, u.role, e.from_status, e.to_status, e.comment, e.created_at
		FROM request_events e
		JOIN users u ON u.id = e.actor_id
		WHERE e.request_id = $1
		ORDER BY e.created_at ASC, e.id ASC
	`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []RequestEvent
	for rows.Next() {
		var event RequestEvent
		var role, fromStatus, toStatus string
		if err := rows.Scan(&event.ID, &event.RequestID, &event.ActorName, &role, &fromStatus, &toStatus, &event.Comment, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.ActorRole = Role(role)
		event.FromStatus = Status(fromStatus)
		event.ToStatus = Status(toStatus)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (a *App) getServiceCatalog(ctx context.Context) ([]ServiceOption, error) {
	rows, err := a.db.Query(ctx, `
		SELECT id, code, name
		FROM services
		WHERE is_active = TRUE
		ORDER BY sort_order ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var services []ServiceOption
	for rows.Next() {
		var service ServiceOption
		if err := rows.Scan(&service.ID, &service.Code, &service.Name); err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (a *App) getRequestServices(ctx context.Context, requestID int64) ([]RequestServiceItem, error) {
	rows, err := a.db.Query(ctx, `
		SELECT id, request_id, service_code, service_name, quantity
		FROM request_services
		WHERE request_id = $1
		ORDER BY id ASC
	`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []RequestServiceItem
	for rows.Next() {
		var item RequestServiceItem
		if err := rows.Scan(&item.ID, &item.RequestID, &item.ServiceCode, &item.ServiceName, &item.Quantity); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) getAttachments(ctx context.Context, requestID int64) ([]RequestAttachment, error) {
	rows, err := a.db.Query(ctx, `
		SELECT a.id, a.request_id, a.uploaded_by, u.name, a.original_name, a.stored_name,
		       a.content_type, a.size_bytes, a.created_at
		FROM request_attachments a
		JOIN users u ON u.id = a.uploaded_by
		WHERE a.request_id = $1 AND a.deleted_at IS NULL
		ORDER BY a.created_at ASC, a.id ASC
	`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attachments []RequestAttachment
	for rows.Next() {
		var attachment RequestAttachment
		if err := rows.Scan(
			&attachment.ID,
			&attachment.RequestID,
			&attachment.UploadedBy,
			&attachment.UploadedByName,
			&attachment.OriginalName,
			&attachment.StoredName,
			&attachment.ContentType,
			&attachment.SizeBytes,
			&attachment.CreatedAt,
		); err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	return attachments, rows.Err()
}

func (a *App) getAttachment(ctx context.Context, requestID, attachmentID int64) (*RequestAttachment, error) {
	row := a.db.QueryRow(ctx, `
		SELECT id, request_id, uploaded_by, original_name, stored_name, content_type, size_bytes, created_at
		FROM request_attachments
		WHERE request_id = $1 AND id = $2 AND deleted_at IS NULL
	`, requestID, attachmentID)

	var attachment RequestAttachment
	if err := row.Scan(
		&attachment.ID,
		&attachment.RequestID,
		&attachment.UploadedBy,
		&attachment.OriginalName,
		&attachment.StoredName,
		&attachment.ContentType,
		&attachment.SizeBytes,
		&attachment.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("вложение не найдено")
		}
		return nil, err
	}
	return &attachment, nil
}

func softDeleteAttachment(ctx context.Context, tx pgx.Tx, requestID, attachmentID, deletedBy int64) (*RequestAttachment, error) {
	row := tx.QueryRow(ctx, `
		UPDATE request_attachments
		SET deleted_at = now(), deleted_by = $3
		WHERE request_id = $1 AND id = $2 AND deleted_at IS NULL
		RETURNING id, request_id, uploaded_by, original_name, stored_name, content_type, size_bytes, created_at
	`, requestID, attachmentID, deletedBy)

	var attachment RequestAttachment
	if err := row.Scan(
		&attachment.ID,
		&attachment.RequestID,
		&attachment.UploadedBy,
		&attachment.OriginalName,
		&attachment.StoredName,
		&attachment.ContentType,
		&attachment.SizeBytes,
		&attachment.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("вложение не найдено")
		}
		return nil, err
	}
	return &attachment, nil
}

type requestScanner interface {
	Scan(dest ...any) error
}

func scanRequest(scanner requestScanner) (RepairRequest, error) {
	var req RepairRequest
	var status string
	var estimate pgtype.Int8
	err := scanner.Scan(
		&req.ID,
		&req.Title,
		&req.Description,
		&req.Address,
		&req.CustomerID,
		&req.CustomerName,
		&req.CustomerEmail,
		&status,
		&estimate,
		&req.AccountantComment,
		&req.CustomerComment,
		&req.WorkerComment,
		&req.CreatedAt,
		&req.UpdatedAt,
	)
	if err != nil {
		return RepairRequest{}, err
	}
	req.Status = Status(status)
	if estimate.Valid {
		value := estimate.Int64
		req.EstimateAmountCents = &value
	}
	return req, nil
}

func canViewRequest(user *User, req RepairRequest) bool {
	switch user.Role {
	case RoleCustomer:
		return req.CustomerID == user.ID
	case RoleAccountant:
		return true
	case RoleWorker:
		return req.Status == StatusApprovedForWorkers || req.Status == StatusInProgress || req.Status == StatusCompleted
	default:
		return false
	}
}

func availableActions(user *User, req *RepairRequest) map[string]bool {
	actions := map[string]bool{
		"attachment_delete": canDeleteAttachments(user, req),
	}
	if user == nil || req == nil {
		return actions
	}

	switch user.Role {
	case RoleAccountant:
		actions["accountant_send"] = req.Status == StatusSentToAccountant || req.Status == StatusReturnedToAccountant
	case RoleCustomer:
		if req.CustomerID == user.ID && req.Status == StatusSentToCustomer {
			actions["customer_approve"] = true
			actions["customer_return"] = true
		}
	case RoleWorker:
		actions["worker_start"] = req.Status == StatusApprovedForWorkers
		actions["worker_complete"] = req.Status == StatusInProgress
	}
	return actions
}

func canDeleteAttachments(user *User, req *RepairRequest) bool {
	if user == nil || req == nil {
		return false
	}

	switch user.Role {
	case RoleCustomer:
		return req.CustomerID == user.ID && req.Status == StatusSentToCustomer
	case RoleAccountant:
		return req.Status == StatusSentToAccountant || req.Status == StatusReturnedToAccountant
	case RoleWorker:
		return req.Status == StatusApprovedForWorkers || req.Status == StatusInProgress
	default:
		return false
	}
}
