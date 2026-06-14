package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

var allowedUploadContentTypes = map[string]string{
	"application/pdf": ".pdf",
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
}

type savedAttachment struct {
	OriginalName string
	StoredName   string
	ContentType  string
	SizeBytes    int64
	Path         string
}

func (a *App) requestAttachments(w http.ResponseWriter, r *http.Request, id int64) {
	user := currentUser(r)
	req, err := a.getRequest(r.Context(), id, user)
	if err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}
	if err := parseRequestForm(r); err != nil {
		http.Error(w, uploadFormErrorMessage(err, a.cfg.MaxUploadTotalBytes), errorStatusFromFormParse(err))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	attachments, err := a.storeUploadedFiles(fileHeadersFromRequest(r), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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

	if err := insertAttachments(r.Context(), tx, req.ID, user.ID, attachments); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	keepFiles = true

	http.Redirect(w, r, fmt.Sprintf("/requests/%d", req.ID), http.StatusSeeOther)
}

func (a *App) requestAttachmentDownload(w http.ResponseWriter, r *http.Request, requestID int64, attachmentIDRaw string) {
	user := currentUser(r)
	if _, err := a.getRequest(r.Context(), requestID, user); err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}

	attachmentID, err := strconv.ParseInt(attachmentIDRaw, 10, 64)
	if err != nil || attachmentID <= 0 {
		http.NotFound(w, r)
		return
	}

	attachment, err := a.getAttachment(r.Context(), requestID, attachmentID)
	if err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}

	file, err := os.Open(filepath.Join(a.cfg.UploadDir, attachment.StoredName))
	if err != nil {
		serverError(w, fmt.Errorf("open attachment %d: %w", attachment.ID, err))
		return
	}
	defer file.Close()

	disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": attachment.OriginalName,
	})
	if disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	w.Header().Set("Content-Type", attachment.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	noCache(w)
	http.ServeContent(w, r, attachment.OriginalName, attachment.CreatedAt, file)
}

func (a *App) requestAttachmentDelete(w http.ResponseWriter, r *http.Request, requestID int64, attachmentIDRaw string) {
	user := currentUser(r)
	req, err := a.getRequest(r.Context(), requestID, user)
	if err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}
	if !canDeleteAttachments(user, req) {
		http.Error(w, errForbidden.Error(), errorStatus(errForbidden))
		return
	}

	attachmentID, err := strconv.ParseInt(attachmentIDRaw, 10, 64)
	if err != nil || attachmentID <= 0 {
		http.NotFound(w, r)
		return
	}

	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	attachment, err := softDeleteAttachment(r.Context(), tx, requestID, attachmentID, user.ID)
	if err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}

	filePath := filepath.Join(a.cfg.UploadDir, attachment.StoredName)
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("delete attachment file", "attachment_id", attachment.ID, "path", filePath, "error", err)
	}

	http.Redirect(w, r, fmt.Sprintf("/requests/%d", requestID), http.StatusSeeOther)
}

func (a *App) storeUploadedFiles(files []*multipart.FileHeader, required bool) ([]savedAttachment, error) {
	if len(files) == 0 {
		if required {
			return nil, fmt.Errorf("выберите хотя бы один файл")
		}
		return nil, nil
	}
	if len(files) > a.cfg.MaxUploadFiles {
		return nil, fmt.Errorf("можно загрузить не более %d файлов", a.cfg.MaxUploadFiles)
	}

	saved := make([]savedAttachment, 0, len(files))
	for _, file := range files {
		attachment, err := a.storeUploadedFile(file)
		if err != nil {
			cleanupSavedFiles(saved)
			return nil, err
		}
		saved = append(saved, attachment)
	}
	return saved, nil
}

func (a *App) storeUploadedFile(fileHeader *multipart.FileHeader) (savedAttachment, error) {
	if fileHeader == nil {
		return savedAttachment{}, fmt.Errorf("получен пустой файл")
	}

	originalName := sanitizeUploadFilename(fileHeader.Filename)
	if originalName == "" {
		return savedAttachment{}, fmt.Errorf("у файла должно быть корректное имя")
	}
	if fileHeader.Size > a.cfg.MaxUploadBytes {
		return savedAttachment{}, fmt.Errorf("файл %q превышает %s", originalName, formatBytes(a.cfg.MaxUploadBytes))
	}

	src, err := fileHeader.Open()
	if err != nil {
		return savedAttachment{}, fmt.Errorf("не удалось открыть файл %q: %w", originalName, err)
	}
	defer src.Close()

	var sniff [512]byte
	readBytes, err := io.ReadFull(src, sniff[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return savedAttachment{}, fmt.Errorf("не удалось прочитать файл %q: %w", originalName, err)
	}
	if readBytes == 0 {
		return savedAttachment{}, fmt.Errorf("файл %q пустой", originalName)
	}

	contentType := http.DetectContentType(sniff[:readBytes])
	extension, ok := allowedUploadContentTypes[contentType]
	if !ok {
		return savedAttachment{}, fmt.Errorf("файл %q должен быть в формате JPG, PNG или PDF", originalName)
	}

	storedName, err := randomStoredName(extension)
	if err != nil {
		return savedAttachment{}, fmt.Errorf("generate file name: %w", err)
	}
	filePath := filepath.Join(a.cfg.UploadDir, storedName)

	dst, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return savedAttachment{}, fmt.Errorf("create upload file: %w", err)
	}

	size, err := writeUploadedFile(dst, src, sniff[:readBytes], a.cfg.MaxUploadBytes)
	closeErr := dst.Close()
	if err != nil {
		_ = os.Remove(filePath)
		return savedAttachment{}, fmt.Errorf("не удалось сохранить файл %q: %w", originalName, err)
	}
	if closeErr != nil {
		_ = os.Remove(filePath)
		return savedAttachment{}, fmt.Errorf("close upload file: %w", closeErr)
	}

	return savedAttachment{
		OriginalName: originalName,
		StoredName:   storedName,
		ContentType:  contentType,
		SizeBytes:    size,
		Path:         filePath,
	}, nil
}

func writeUploadedFile(dst *os.File, src multipart.File, firstChunk []byte, maxBytes int64) (int64, error) {
	size := int64(len(firstChunk))
	if _, err := dst.Write(firstChunk); err != nil {
		return 0, err
	}

	remainingLimit := maxBytes + 1 - size
	if remainingLimit < 0 {
		return size, fmt.Errorf("file too large")
	}
	written, err := io.Copy(dst, io.LimitReader(src, remainingLimit))
	if err != nil {
		return size, err
	}
	size += written
	if size > maxBytes {
		return size, fmt.Errorf("file too large")
	}
	return size, nil
}

func insertAttachments(ctx context.Context, tx pgx.Tx, requestID, userID int64, attachments []savedAttachment) error {
	for _, attachment := range attachments {
		_, err := tx.Exec(ctx, `
			INSERT INTO request_attachments (request_id, uploaded_by, original_name, stored_name, content_type, size_bytes)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, requestID, userID, attachment.OriginalName, attachment.StoredName, attachment.ContentType, attachment.SizeBytes)
		if err != nil {
			return err
		}
	}
	return nil
}

func fileHeadersFromRequest(r *http.Request) []*multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	return r.MultipartForm.File["attachments"]
}

func cleanupSavedFiles(files []savedAttachment) {
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		_ = os.Remove(file.Path)
	}
}

func sanitizeUploadFilename(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		switch {
		case r == 0:
			return -1
		case r < 32:
			return -1
		default:
			return r
		}
	}, name)
	if len([]rune(name)) > 255 {
		name = string([]rune(name)[:255])
	}
	if name == "" || name == "." {
		return ""
	}
	return name
}

func randomStoredName(extension string) (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer[:]) + extension, nil
}

func uploadFormErrorMessage(err error, maxTotalBytes int64) string {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return fmt.Sprintf("суммарный размер файлов не должен превышать %s", formatBytes(maxTotalBytes))
	}
	return "не удалось обработать форму"
}

func errorStatusFromFormParse(err error) int {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
