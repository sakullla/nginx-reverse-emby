package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrClientPackageNotFound = errors.New("client package not found")

type ClientPackage struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Kind        string `json:"kind"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
	Notes       string `json:"notes"`
	CreatedAt   string `json:"created_at"`
}

type ClientPackageInput struct {
	ID          *string `json:"id,omitempty"`
	Version     *string `json:"version,omitempty"`
	Platform    *string `json:"platform,omitempty"`
	Arch        *string `json:"arch,omitempty"`
	Kind        *string `json:"kind,omitempty"`
	DownloadURL *string `json:"download_url,omitempty"`
	SHA256      *string `json:"sha256,omitempty"`
	Notes       *string `json:"notes,omitempty"`
	CreatedAt   *string `json:"created_at,omitempty"`
}

type ClientPackageQuery struct {
	Platform string
	Arch     string
	Kind     string
}

type clientPackageService struct {
	store storage.Store
	now   func() time.Time
}

func NewClientPackageService(store storage.Store) *clientPackageService {
	return &clientPackageService{
		store: store,
		now:   time.Now,
	}
}

func (s *clientPackageService) List(ctx context.Context) ([]ClientPackage, error) {
	rows, err := s.store.ListClientPackages(ctx)
	if err != nil {
		return nil, err
	}

	packages := make([]ClientPackage, 0, len(rows))
	for _, row := range rows {
		packages = append(packages, clientPackageFromRow(row))
	}
	return packages, nil
}

func (s *clientPackageService) Create(ctx context.Context, input ClientPackageInput) (ClientPackage, error) {
	current, err := s.List(ctx)
	if err != nil {
		return ClientPackage{}, err
	}

	pkg, err := normalizeClientPackageInput(input, ClientPackage{}, s.now().UTC().Format(time.RFC3339), true)
	if err != nil {
		return ClientPackage{}, err
	}
	if pkg.ID == "" {
		pkg.ID = generatedClientPackageID(pkg)
	}

	for _, existing := range current {
		if existing.ID == pkg.ID {
			return ClientPackage{}, fmt.Errorf("%w: client package id already exists: %s", ErrInvalidArgument, pkg.ID)
		}
	}

	rows := make([]storage.ClientPackageRow, 0, len(current)+1)
	for _, existing := range current {
		rows = append(rows, clientPackageToRow(existing))
	}
	rows = append(rows, clientPackageToRow(pkg))
	if err := s.store.SaveClientPackages(ctx, rows); err != nil {
		return ClientPackage{}, err
	}
	return pkg, nil
}

func (s *clientPackageService) Update(ctx context.Context, id string, input ClientPackageInput) (ClientPackage, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ClientPackage{}, ErrClientPackageNotFound
	}

	current, err := s.List(ctx)
	if err != nil {
		return ClientPackage{}, err
	}

	targetIndex := -1
	var existing ClientPackage
	for i, item := range current {
		if item.ID == id {
			targetIndex = i
			existing = item
			break
		}
	}
	if targetIndex < 0 {
		return ClientPackage{}, ErrClientPackageNotFound
	}

	next, err := normalizeClientPackageInput(input, existing, existing.CreatedAt, false)
	if err != nil {
		return ClientPackage{}, err
	}
	next.ID = existing.ID
	next.CreatedAt = existing.CreatedAt
	current[targetIndex] = next

	rows := make([]storage.ClientPackageRow, 0, len(current))
	for _, item := range current {
		rows = append(rows, clientPackageToRow(item))
	}
	if err := s.store.SaveClientPackages(ctx, rows); err != nil {
		return ClientPackage{}, err
	}
	return next, nil
}

func (s *clientPackageService) Delete(ctx context.Context, id string) (ClientPackage, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ClientPackage{}, ErrClientPackageNotFound
	}

	current, err := s.List(ctx)
	if err != nil {
		return ClientPackage{}, err
	}

	targetIndex := -1
	var deleted ClientPackage
	for i, item := range current {
		if item.ID == id {
			targetIndex = i
			deleted = item
			break
		}
	}
	if targetIndex < 0 {
		return ClientPackage{}, ErrClientPackageNotFound
	}

	next := append([]ClientPackage(nil), current[:targetIndex]...)
	next = append(next, current[targetIndex+1:]...)

	rows := make([]storage.ClientPackageRow, 0, len(next))
	for _, item := range next {
		rows = append(rows, clientPackageToRow(item))
	}
	if err := s.store.SaveClientPackages(ctx, rows); err != nil {
		return ClientPackage{}, err
	}
	return deleted, nil
}

func (s *clientPackageService) Latest(ctx context.Context, query ClientPackageQuery) (ClientPackage, error) {
	platform := strings.ToLower(strings.TrimSpace(query.Platform))
	arch := strings.ToLower(strings.TrimSpace(query.Arch))
	kind := strings.ToLower(strings.TrimSpace(query.Kind))

	var latest ClientPackage
	found := false
	packages, err := s.List(ctx)
	if err != nil {
		return ClientPackage{}, err
	}
	for _, pkg := range packages {
		if pkg.Platform != platform || pkg.Arch != arch || pkg.Kind != kind {
			continue
		}
		if !found || compareClientPackageVersions(pkg.Version, latest.Version) > 0 {
			latest = pkg
			found = true
		}
	}
	if !found {
		return ClientPackage{}, ErrClientPackageNotFound
	}
	return latest, nil
}

func normalizeClientPackageInput(input ClientPackageInput, fallback ClientPackage, createdAt string, allowID bool) (ClientPackage, error) {
	id := strings.TrimSpace(fallback.ID)
	if allowID && input.ID != nil {
		id = strings.TrimSpace(*input.ID)
	}

	version := trimmedInput(input.Version, fallback.Version)
	if version == "" {
		return ClientPackage{}, fmt.Errorf("%w: version is required", ErrInvalidArgument)
	}

	platform := lowerTrimmedInput(input.Platform, fallback.Platform)
	if !validClientPackageValue(platform, []string{"windows", "macos", "android", "cloudflare_worker"}) {
		return ClientPackage{}, fmt.Errorf("%w: platform must be windows, macos, android, or cloudflare_worker", ErrInvalidArgument)
	}

	arch := lowerTrimmedInput(input.Arch, fallback.Arch)
	if !validClientPackageValue(arch, []string{"amd64", "arm64", "universal", "script"}) {
		return ClientPackage{}, fmt.Errorf("%w: arch must be amd64, arm64, universal, or script", ErrInvalidArgument)
	}

	kind := lowerTrimmedInput(input.Kind, fallback.Kind)
	if !validClientPackageValue(kind, []string{"flutter_gui", "go_agent", "worker_script"}) {
		return ClientPackage{}, fmt.Errorf("%w: kind must be flutter_gui, go_agent, or worker_script", ErrInvalidArgument)
	}

	if platform == "cloudflare_worker" && (arch != "script" || kind != "worker_script") {
		return ClientPackage{}, fmt.Errorf("%w: cloudflare_worker packages must use arch=script and kind=worker_script", ErrInvalidArgument)
	}

	downloadURL := trimmedInput(input.DownloadURL, fallback.DownloadURL)
	if !validHTTPSURL(downloadURL) {
		return ClientPackage{}, fmt.Errorf("%w: download_url must be an absolute HTTPS URL", ErrInvalidArgument)
	}

	sha256 := lowerTrimmedInput(input.SHA256, fallback.SHA256)
	if !validSHA256(sha256) {
		return ClientPackage{}, fmt.Errorf("%w: sha256 must be 64 hex characters", ErrInvalidArgument)
	}

	if createdAt == "" {
		createdAt = fallback.CreatedAt
	}
	if input.CreatedAt != nil && allowID {
		createdAt = strings.TrimSpace(*input.CreatedAt)
	}

	return ClientPackage{
		ID:          id,
		Version:     version,
		Platform:    platform,
		Arch:        arch,
		Kind:        kind,
		DownloadURL: downloadURL,
		SHA256:      sha256,
		Notes:       trimmedInput(input.Notes, fallback.Notes),
		CreatedAt:   createdAt,
	}, nil
}

func trimmedInput(value *string, fallback string) string {
	if value == nil {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(*value)
}

func lowerTrimmedInput(value *string, fallback string) string {
	return strings.ToLower(trimmedInput(value, fallback))
}

func validClientPackageValue(value string, allowed []string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func validHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && parsed.Host != ""
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func generatedClientPackageID(pkg ClientPackage) string {
	id := fmt.Sprintf("%s-%s-%s-%s", pkg.Kind, pkg.Platform, pkg.Arch, pkg.Version)
	replacer := strings.NewReplacer(".", "-", "+", "-", "/", "-")
	return replacer.Replace(id)
}

func compareClientPackageVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	length := len(leftParts)
	if len(rightParts) > length {
		length = len(rightParts)
	}
	for i := 0; i < length; i++ {
		leftValue := clientPackageVersionPart(leftParts, i)
		rightValue := clientPackageVersionPart(rightParts, i)
		if leftValue > rightValue {
			return 1
		}
		if leftValue < rightValue {
			return -1
		}
	}
	return strings.Compare(left, right)
}

func clientPackageVersionPart(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	part := parts[index]
	if dash := strings.Index(part, "-"); dash >= 0 {
		part = part[:dash]
	}
	value, err := strconv.Atoi(part)
	if err != nil {
		return 0
	}
	return value
}

func clientPackageFromRow(row storage.ClientPackageRow) ClientPackage {
	return ClientPackage{
		ID:          row.ID,
		Version:     row.Version,
		Platform:    row.Platform,
		Arch:        row.Arch,
		Kind:        row.Kind,
		DownloadURL: row.DownloadURL,
		SHA256:      row.SHA256,
		Notes:       row.Notes,
		CreatedAt:   row.CreatedAt,
	}
}

func clientPackageToRow(pkg ClientPackage) storage.ClientPackageRow {
	return storage.ClientPackageRow{
		ID:          pkg.ID,
		Version:     pkg.Version,
		Platform:    pkg.Platform,
		Arch:        pkg.Arch,
		Kind:        pkg.Kind,
		DownloadURL: pkg.DownloadURL,
		SHA256:      pkg.SHA256,
		Notes:       pkg.Notes,
		CreatedAt:   pkg.CreatedAt,
	}
}
