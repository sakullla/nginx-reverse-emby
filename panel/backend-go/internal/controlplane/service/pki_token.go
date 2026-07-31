package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	PKIEnrollmentTokenScopeNewAgent          = "new_agent"
	PKIEnrollmentTokenScopeBoundReenrollment = "bound_reenrollment"

	defaultPKIEnrollmentTokenTTL = 10 * time.Minute
	pkiEnrollmentTokenBytes      = 32
)

var (
	ErrPKIEnrollmentTokenRequest  = errors.New("invalid PKI enrollment token request")
	ErrPKIEnrollmentTokenRejected = errors.New("PKI enrollment token rejected")
)

type PKITransactionStore interface {
	WithPKITransaction(context.Context, func(*storage.PKITransaction) error) error
}

type PKIIDGenerator func() (string, error)

type PKITokenServiceOptions struct {
	Store    PKITransactionStore
	Clock    func() time.Time
	Random   io.Reader
	NewID    PKIIDGenerator
	TokenTTL time.Duration
}

type PKITokenService struct {
	store    PKITransactionStore
	clock    func() time.Time
	random   io.Reader
	newID    PKIIDGenerator
	tokenTTL time.Duration
}

type PKIEnrollmentTokenRequest struct {
	Scope        string
	BoundAgentID string
	CreatedBy    string
}

// PKIEnrollmentToken is the only value that carries a plaintext enrollment
// secret. Callers must return or display it once; storage only receives its
// SHA-256 digest.
type PKIEnrollmentToken struct {
	Token        string
	Scope        string
	BoundAgentID string
	ExpiresAt    time.Time
}

func NewPKITokenService(options PKITokenServiceOptions) (*PKITokenService, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrPKIEnrollmentTokenRequest)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.TokenTTL == 0 {
		options.TokenTTL = defaultPKIEnrollmentTokenTTL
	}
	if options.TokenTTL <= 0 {
		return nil, fmt.Errorf("%w: token TTL must be positive", ErrPKIEnrollmentTokenRequest)
	}
	service := &PKITokenService{
		store:    options.Store,
		clock:    options.Clock,
		random:   options.Random,
		newID:    options.NewID,
		tokenTTL: options.TokenTTL,
	}
	if service.newID == nil {
		service.newID = service.randomID
	}
	return service, nil
}

func (s *PKITokenService) Create(ctx context.Context, request PKIEnrollmentTokenRequest) (PKIEnrollmentToken, error) {
	request.Scope = strings.TrimSpace(request.Scope)
	request.BoundAgentID = strings.TrimSpace(request.BoundAgentID)
	request.CreatedBy = strings.TrimSpace(request.CreatedBy)
	if request.CreatedBy == "" {
		return PKIEnrollmentToken{}, fmt.Errorf("%w: creator is required", ErrPKIEnrollmentTokenRequest)
	}
	switch request.Scope {
	case PKIEnrollmentTokenScopeNewAgent:
		if request.BoundAgentID != "" {
			return PKIEnrollmentToken{}, fmt.Errorf("%w: new-agent token cannot be owner-bound", ErrPKIEnrollmentTokenRequest)
		}
	case PKIEnrollmentTokenScopeBoundReenrollment:
		if request.BoundAgentID == "" {
			return PKIEnrollmentToken{}, fmt.Errorf("%w: bound re-enrollment token requires an agent owner", ErrPKIEnrollmentTokenRequest)
		}
	default:
		return PKIEnrollmentToken{}, fmt.Errorf("%w: unsupported scope", ErrPKIEnrollmentTokenRequest)
	}

	secret := make([]byte, pkiEnrollmentTokenBytes)
	if _, err := io.ReadFull(s.random, secret); err != nil {
		return PKIEnrollmentToken{}, fmt.Errorf("generate PKI enrollment token: %w", err)
	}
	tokenID, err := s.newID()
	if err != nil {
		return PKIEnrollmentToken{}, fmt.Errorf("generate PKI enrollment token identifier: %w", err)
	}
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return PKIEnrollmentToken{}, fmt.Errorf("generate PKI enrollment token identifier: empty identifier")
	}
	now := s.clock().UTC()
	expiresAt := now.Add(s.tokenTTL)
	digest := sha256.Sum256(secret)
	row := storage.PKIEnrollmentTokenRow{
		ID:                tokenID,
		TokenDigestSHA256: hex.EncodeToString(digest[:]),
		Scope:             request.Scope,
		BoundAgentID:      request.BoundAgentID,
		ExpiresAt:         expiresAt,
		CreatedBy:         request.CreatedBy,
		CreatedAt:         now,
	}
	if err := s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		return tx.CreatePKIEnrollmentToken(ctx, row)
	}); err != nil {
		return PKIEnrollmentToken{}, err
	}
	return PKIEnrollmentToken{
		Token:        hex.EncodeToString(secret),
		Scope:        request.Scope,
		BoundAgentID: request.BoundAgentID,
		ExpiresAt:    expiresAt,
	}, nil
}

func digestPKIEnrollmentToken(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != pkiEnrollmentTokenBytes*2 {
		return "", ErrPKIEnrollmentTokenRejected
	}
	secret, err := hex.DecodeString(value)
	if err != nil || len(secret) != pkiEnrollmentTokenBytes {
		return "", ErrPKIEnrollmentTokenRejected
	}
	digest := sha256.Sum256(secret)
	return hex.EncodeToString(digest[:]), nil
}

func (s *PKITokenService) randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(s.random, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
