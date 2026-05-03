package http

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

var backupImportMaxBytes int64 = 32 << 20

func (d Dependencies) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	includeParam := r.URL.Query().Get("include")
	var body []byte
	var filename string
	var err error
	if includeParam != "" {
		opts := parseExportOptions(includeParam)
		body, filename, err = d.BackupService.ExportSelective(r.Context(), opts)
	} else {
		body, filename, err = d.BackupService.Export(r.Context())
	}
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

func parseExportOptions(include string) service.BackupExportOptions {
	parts := map[string]bool{}
	for _, p := range strings.Split(include, ",") {
		parts[strings.TrimSpace(p)] = true
	}
	return service.BackupExportOptions{
		Agents:           parts["agents"],
		HTTPRules:        parts["http_rules"],
		L4Rules:          parts["l4_rules"],
		RelayListeners:   parts["relay_listeners"],
		Certificates:     parts["certificates"],
		VersionPolicies:  parts["version_policies"],
		TrafficPolicies:  parts["traffic_policies"],
		TrafficBaselines: parts["traffic_baselines"],
	}
}

func (d Dependencies) handleBackupResourceCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	counts, err := d.BackupService.ResourceCounts(r.Context())
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"counts": counts,
	})
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

func (d Dependencies) handleBackupImportPreview(w http.ResponseWriter, r *http.Request) {
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

	result, err := d.BackupService.Preview(r.Context(), body)
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
