package http

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

var backupImportMaxBytes int64 = 32 << 20

func (d Dependencies) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	body, filename, err := d.BackupService.Export(r.Context())
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", backupContentDisposition(filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (d Dependencies) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, backupImportMaxBytes)
	file, _, err := r.FormFile("file")
	if err != nil {
		if isBackupImportTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorPayload("backup file too large"))
			return
		}
		writeJSON(w, http.StatusBadRequest, errorPayload("missing backup file"))
		return
	}
	defer file.Close()

	body, err := io.ReadAll(file)
	if err != nil {
		if isBackupImportTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorPayload("backup file too large"))
			return
		}
		writeJSON(w, http.StatusBadRequest, errorPayload("failed to read backup file"))
		return
	}
	result, err := d.BackupService.Import(r.Context(), body)
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"manifest": result.Manifest,
		"summary":  result.Summary,
		"report":   result.Report,
	})
}

func backupContentDisposition(filename string) string {
	safeName := sanitizeBackupFilename(filename)
	if safeName == "" {
		safeName = "nre-backup.tar.gz"
	}
	value := mime.FormatMediaType("attachment", map[string]string{"filename": safeName})
	if value == "" {
		return `attachment; filename="nre-backup.tar.gz"`
	}
	return value
}

func isBackupImportTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large") || strings.Contains(err.Error(), "multipart: message too large")
}

func sanitizeBackupFilename(filename string) string {
	trimmed := strings.TrimSpace(filename)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(trimmed))
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('_')
	}
	return builder.String()
}
