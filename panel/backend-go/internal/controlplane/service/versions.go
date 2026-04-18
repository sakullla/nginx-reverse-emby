package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrVersionPolicyNotFound = errors.New("version policy not found")

type VersionPackage struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}

type VersionPolicy struct {
	ID             string           `json:"id"`
	Channel        string           `json:"channel"`
	DesiredVersion string           `json:"desired_version"`
	Packages       []VersionPackage `json:"packages"`
	Tags           []string         `json:"tags"`
}

type VersionPolicyInput struct {
	ID             *string           `json:"id,omitempty"`
	Channel        *string           `json:"channel,omitempty"`
	DesiredVersion *string           `json:"desired_version,omitempty"`
	Packages       *[]VersionPackage `json:"packages,omitempty"`
	Tags           *[]string         `json:"tags,omitempty"`
}

type versionPolicyService struct {
	store storage.Store
	now   func() time.Time
}

func NewVersionPolicyService(store storage.Store) *versionPolicyService {
	return &versionPolicyService{
		store: store,
		now:   time.Now,
	}
}

func (s *versionPolicyService) List(ctx context.Context) ([]VersionPolicy, error) {
	rows, err := s.store.ListVersionPolicies(ctx)
	if err != nil {
		return nil, err
	}

	policies := make([]VersionPolicy, 0, len(rows))
	for _, row := range rows {
		policies = append(policies, versionPolicyFromRow(row))
	}
	return policies, nil
}

func (s *versionPolicyService) Create(ctx context.Context, input VersionPolicyInput) (VersionPolicy, error) {
	current, err := s.List(ctx)
	if err != nil {
		return VersionPolicy{}, err
	}

	policy, err := normalizeVersionPolicyInput(input, VersionPolicy{}, s.generatedPolicyID(input))
	if err != nil {
		return VersionPolicy{}, err
	}

	for _, existing := range current {
		if existing.ID == policy.ID {
			return VersionPolicy{}, fmt.Errorf("%w: version policy id already exists: %s", ErrInvalidArgument, policy.ID)
		}
	}

	rows := make([]storage.VersionPolicyRow, 0, len(current)+1)
	for _, existing := range current {
		rows = append(rows, versionPolicyToRow(existing))
	}
	rows = append(rows, versionPolicyToRow(policy))
	if err := s.store.SaveVersionPolicies(ctx, rows); err != nil {
		return VersionPolicy{}, err
	}
	return policy, nil
}

func (s *versionPolicyService) Update(ctx context.Context, id string, input VersionPolicyInput) (VersionPolicy, error) {
	current, err := s.List(ctx)
	if err != nil {
		return VersionPolicy{}, err
	}

	targetIndex := -1
	var existing VersionPolicy
	for i, item := range current {
		if item.ID == id {
			targetIndex = i
			existing = item
			break
		}
	}
	if targetIndex < 0 {
		return VersionPolicy{}, ErrVersionPolicyNotFound
	}

	next, err := normalizeVersionPolicyInput(input, existing, existing.ID)
	if err != nil {
		return VersionPolicy{}, err
	}
	current[targetIndex] = next

	rows := make([]storage.VersionPolicyRow, 0, len(current))
	for _, item := range current {
		rows = append(rows, versionPolicyToRow(item))
	}
	if err := s.store.SaveVersionPolicies(ctx, rows); err != nil {
		return VersionPolicy{}, err
	}
	return next, nil
}

func (s *versionPolicyService) Delete(ctx context.Context, id string) (VersionPolicy, error) {
	current, err := s.List(ctx)
	if err != nil {
		return VersionPolicy{}, err
	}

	targetIndex := -1
	var deleted VersionPolicy
	for i, item := range current {
		if item.ID == id {
			targetIndex = i
			deleted = item
			break
		}
	}
	if targetIndex < 0 {
		return VersionPolicy{}, ErrVersionPolicyNotFound
	}

	next := append([]VersionPolicy(nil), current[:targetIndex]...)
	next = append(next, current[targetIndex+1:]...)

	rows := make([]storage.VersionPolicyRow, 0, len(next))
	for _, item := range next {
		rows = append(rows, versionPolicyToRow(item))
	}
	if err := s.store.SaveVersionPolicies(ctx, rows); err != nil {
		return VersionPolicy{}, err
	}
	return deleted, nil
}

func (s *versionPolicyService) generatedPolicyID(input VersionPolicyInput) string {
	if input.ID != nil && strings.TrimSpace(*input.ID) != "" {
		return strings.TrimSpace(*input.ID)
	}
	if input.Channel != nil && strings.TrimSpace(*input.Channel) != "" {
		return strings.TrimSpace(*input.Channel)
	}
	return fmt.Sprintf("policy-%d", s.now().UTC().Unix())
}

func normalizeVersionPolicyInput(input VersionPolicyInput, fallback VersionPolicy, suggestedID string) (VersionPolicy, error) {
	id := strings.TrimSpace(pointerString(input.ID))
	if id == "" {
		id = strings.TrimSpace(fallback.ID)
	}
	if id == "" {
		id = strings.TrimSpace(suggestedID)
	}
	if id == "" {
		id = "default"
	}

	channel := strings.TrimSpace(pointerString(input.Channel))
	if channel == "" {
		channel = strings.TrimSpace(fallback.Channel)
	}
	if channel == "" {
		channel = "stable"
	}

	desiredVersion := strings.TrimSpace(pointerString(input.DesiredVersion))
	if desiredVersion == "" {
		desiredVersion = strings.TrimSpace(fallback.DesiredVersion)
	}
	if desiredVersion == "" {
		return VersionPolicy{}, fmt.Errorf("%w: desired_version is required", ErrInvalidArgument)
	}

	packages := append([]VersionPackage(nil), fallback.Packages...)
	if input.Packages != nil {
		packages = normalizeVersionPackages(*input.Packages)
	}

	tags := append([]string(nil), fallback.Tags...)
	if input.Tags != nil {
		tags = normalizeTags(*input.Tags)
	}

	return VersionPolicy{
		ID:             id,
		Channel:        channel,
		DesiredVersion: desiredVersion,
		Packages:       packages,
		Tags:           tags,
	}, nil
}

func normalizeVersionPackages(packages []VersionPackage) []VersionPackage {
	normalized := make([]VersionPackage, 0, len(packages))
	for _, pkg := range packages {
		platform := strings.TrimSpace(pkg.Platform)
		url := strings.TrimSpace(pkg.URL)
		sha := strings.TrimSpace(pkg.SHA256)
		if platform == "" || url == "" || sha == "" {
			continue
		}
		normalized = append(normalized, VersionPackage{
			Platform: platform,
			URL:      url,
			SHA256:   sha,
		})
	}
	return normalized
}

func versionPolicyFromRow(row storage.VersionPolicyRow) VersionPolicy {
	policy := VersionPolicy{
		ID:             row.ID,
		Channel:        defaultString(row.Channel, "stable"),
		DesiredVersion: row.DesiredVersion,
		Tags:           parseStringArray(row.TagsJSON),
		Packages:       []VersionPackage{},
	}

	if err := json.Unmarshal([]byte(defaultString(row.PackagesJSON, "[]")), &policy.Packages); err != nil {
		policy.Packages = []VersionPackage{}
	}
	policy.Packages = normalizeVersionPackages(policy.Packages)
	return policy
}

func versionPolicyToRow(policy VersionPolicy) storage.VersionPolicyRow {
	return storage.VersionPolicyRow{
		ID:             policy.ID,
		Channel:        policy.Channel,
		DesiredVersion: policy.DesiredVersion,
		PackagesJSON:   marshalJSON(policy.Packages, "[]"),
		TagsJSON:       marshalJSON(policy.Tags, "[]"),
	}
}
