package cloudflare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

type DNSRecordClient interface {
	FindZone(context.Context, string) (Zone, error)
	ListTXTRecords(context.Context, string, string) ([]TXTRecord, error)
	CreateTXTRecord(context.Context, string, string, string) (TXTRecord, error)
	DeleteRecord(context.Context, string, string) error
}

type ChallengeIntentStore interface {
	SaveChallengeIntent(context.Context, acmeflow.ChallengeIntent) error
	SetChallengeRecordID(context.Context, string, string) error
	CompleteChallengeIntent(context.Context, string) error
	ListChallengeIntents(context.Context) ([]acmeflow.ChallengeIntent, error)
}

type DNS01Config struct {
	Client      DNSRecordClient
	Propagation DNSPropagation
	Intents     ChallengeIntentStore
}

type DNS01Solver struct {
	client      DNSRecordClient
	propagation DNSPropagation
	intents     ChallengeIntentStore

	provisionMu sync.Mutex
	sessionsMu  sync.Mutex
	sessions    map[string]dns01Session
}

type dns01Session struct {
	zone        Zone
	intent      acmeflow.ChallengeIntent
	preexisting bool
}

var _ acmeflow.ChallengeSolver = (*DNS01Solver)(nil)

func NewDNS01Solver(config DNS01Config) (*DNS01Solver, error) {
	if config.Client == nil || config.Propagation == nil || config.Intents == nil {
		return nil, providerError(acmeflow.CategoryProtocol, "dns01_config", errors.New("DNS-01 client, propagation, and intent store are required"))
	}
	return &DNS01Solver{
		client:      config.Client,
		propagation: config.Propagation,
		intents:     config.Intents,
		sessions:    make(map[string]dns01Session),
	}, nil
}

func (*DNS01Solver) ChallengeType() string {
	return acmeflow.ChallengeDNS01
}

func (solver *DNS01Solver) Present(ctx context.Context, challenge acmeflow.Challenge) error {
	const operation = "dns01_present"
	if solver == nil {
		return providerError(acmeflow.CategoryProtocol, operation, errors.New("DNS-01 solver is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return err
	}
	challengeName, err := dns01ChallengeName(challenge)
	if err != nil {
		return providerError(acmeflow.CategoryChallenge, operation, err)
	}
	solver.provisionMu.Lock()
	defer solver.provisionMu.Unlock()

	target, err := solver.propagation.ResolveCNAME(ctx, challengeName)
	if err != nil {
		return err
	}
	zone, err := solver.client.FindZone(ctx, target)
	if err != nil {
		return err
	}
	intent, err := acmeflow.NewChallengeIntent(zone.Name, target, challenge.DNSValue)
	if err != nil {
		return providerError(acmeflow.CategoryChallenge, operation, err)
	}
	existing, err := solver.findIntent(ctx, intent.ID)
	if err != nil {
		return providerError(acmeflow.CategoryChallenge, operation, err)
	}
	if existing != nil {
		if existing.Status == acmeflow.ChallengeIntentCompleted {
			return providerError(acmeflow.CategoryChallenge, operation, errors.New("DNS challenge intent is already complete"))
		}
		intent = *existing
		return solver.resumePresent(ctx, challenge, zone, intent)
	}
	preexisting, err := solver.matchingRecords(ctx, zone, intent)
	if err != nil {
		return err
	}
	if len(preexisting) > 0 {
		solver.setSession(challenge, dns01Session{zone: zone, intent: intent, preexisting: true})
		return nil
	}
	if err := solver.intents.SaveChallengeIntent(ctx, intent); err != nil {
		return providerError(acmeflow.CategoryChallenge, operation, err)
	}
	record, err := solver.client.CreateTXTRecord(ctx, zone.ID, intent.FQDN, challenge.DNSValue)
	if err != nil {
		return err
	}
	intent.RecordID = record.ID
	solver.setSession(challenge, dns01Session{zone: zone, intent: intent})
	if err := solver.intents.SetChallengeRecordID(ctx, intent.ID, record.ID); err != nil {
		return providerError(acmeflow.CategoryChallenge, operation, err)
	}
	return nil
}

func (solver *DNS01Solver) Wait(ctx context.Context, challenge acmeflow.Challenge) error {
	const operation = "dns01_wait"
	if solver == nil {
		return providerError(acmeflow.CategoryProtocol, operation, errors.New("DNS-01 solver is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return err
	}
	if _, err := dns01ChallengeName(challenge); err != nil {
		return providerError(acmeflow.CategoryChallenge, operation, err)
	}
	session, exists := solver.session(challenge)
	if !exists {
		return providerError(acmeflow.CategoryChallenge, operation, errors.New("DNS challenge was not presented"))
	}
	return solver.propagation.WaitTXT(ctx, session.intent.FQDN, challenge.DNSValue, session.intent.Zone)
}

func (solver *DNS01Solver) Cleanup(ctx context.Context, challenge acmeflow.Challenge) error {
	const operation = "dns01_cleanup"
	if solver == nil {
		return providerError(acmeflow.CategoryProtocol, operation, errors.New("DNS-01 solver is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return err
	}
	challengeName, err := dns01ChallengeName(challenge)
	if err != nil {
		return providerError(acmeflow.CategoryCleanup, operation, err)
	}
	solver.provisionMu.Lock()
	defer solver.provisionMu.Unlock()

	session, exists := solver.session(challenge)
	if !exists {
		target, resolveErr := solver.propagation.ResolveCNAME(ctx, challengeName)
		if resolveErr != nil {
			return resolveErr
		}
		zone, zoneErr := solver.client.FindZone(ctx, target)
		if zoneErr != nil {
			return zoneErr
		}
		intent, intentErr := acmeflow.NewChallengeIntent(zone.Name, target, challenge.DNSValue)
		if intentErr != nil {
			return providerError(acmeflow.CategoryCleanup, operation, intentErr)
		}
		existing, findErr := solver.findIntent(ctx, intent.ID)
		if findErr != nil {
			return providerError(acmeflow.CategoryCleanup, operation, findErr)
		}
		if existing == nil || existing.Status == acmeflow.ChallengeIntentCompleted {
			solver.deleteSession(challenge)
			return nil
		}
		session = dns01Session{zone: zone, intent: *existing}
	}
	if session.intent.Status == acmeflow.ChallengeIntentCompleted {
		solver.deleteSession(challenge)
		return nil
	}
	if session.preexisting {
		solver.deleteSession(challenge)
		return nil
	}
	if err := solver.cleanupIntent(ctx, session.zone, session.intent); err != nil {
		return err
	}
	solver.deleteSession(challenge)
	return nil
}

// RecoverPending removes only records owned by pending intents. A known record
// ID is authoritative; the exact name/content hash fallback is used solely for
// the crash window between provider creation and record-ID persistence.
func (solver *DNS01Solver) RecoverPending(ctx context.Context) error {
	const operation = "dns01_recover"
	if solver == nil {
		return providerError(acmeflow.CategoryProtocol, operation, errors.New("DNS-01 solver is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return err
	}
	solver.provisionMu.Lock()
	defer solver.provisionMu.Unlock()
	intents, err := solver.intents.ListChallengeIntents(ctx)
	if err != nil {
		return providerError(acmeflow.CategoryCleanup, operation, err)
	}
	sort.SliceStable(intents, func(i, j int) bool { return intents[i].ID < intents[j].ID })
	for _, intent := range intents {
		if intent.Status != acmeflow.ChallengeIntentPending {
			continue
		}
		zone, err := solver.client.FindZone(ctx, intent.Zone)
		if err != nil {
			return err
		}
		if err := solver.cleanupIntent(ctx, zone, intent); err != nil {
			return err
		}
	}
	return nil
}

func (solver *DNS01Solver) resumePresent(ctx context.Context, challenge acmeflow.Challenge, zone Zone, intent acmeflow.ChallengeIntent) error {
	const operation = "dns01_present"
	if intent.RecordID != "" {
		solver.setSession(challenge, dns01Session{zone: zone, intent: intent})
		return nil
	}
	records, err := solver.matchingRecords(ctx, zone, intent)
	if err != nil {
		return err
	}
	var record TXTRecord
	switch len(records) {
	case 0:
		record, err = solver.client.CreateTXTRecord(ctx, zone.ID, intent.FQDN, challenge.DNSValue)
		if err != nil {
			return err
		}
	case 1:
		record = records[0]
	default:
		return providerError(acmeflow.CategoryChallenge, operation, errors.New("DNS challenge recovery is ambiguous"))
	}
	intent.RecordID = record.ID
	solver.setSession(challenge, dns01Session{zone: zone, intent: intent})
	if err := solver.intents.SetChallengeRecordID(ctx, intent.ID, record.ID); err != nil {
		return providerError(acmeflow.CategoryChallenge, operation, err)
	}
	return nil
}

func (solver *DNS01Solver) cleanupIntent(ctx context.Context, zone Zone, intent acmeflow.ChallengeIntent) error {
	const operation = "dns01_cleanup"
	zoneName, err := normalizeDNSName(zone.Name)
	if err != nil || zoneName != intent.Zone {
		return providerError(acmeflow.CategoryCleanup, operation, errors.New("persisted DNS challenge zone is unavailable"))
	}
	if intent.RecordID == "" {
		records, err := solver.matchingRecords(ctx, zone, intent)
		if err != nil {
			return err
		}
		switch len(records) {
		case 0:
			return providerError(acmeflow.CategoryCleanup, operation, errors.New("DNS cleanup recovery record is not visible"))
		case 1:
			intent.RecordID = records[0].ID
			if err := solver.intents.SetChallengeRecordID(ctx, intent.ID, intent.RecordID); err != nil {
				return providerError(acmeflow.CategoryCleanup, operation, err)
			}
		default:
			return providerError(acmeflow.CategoryCleanup, operation, errors.New("DNS cleanup recovery is ambiguous"))
		}
	}
	if err := solver.client.DeleteRecord(ctx, zone.ID, intent.RecordID); err != nil {
		return err
	}
	return solver.completeIntent(ctx, intent.ID)
}

func (solver *DNS01Solver) matchingRecords(ctx context.Context, zone Zone, intent acmeflow.ChallengeIntent) ([]TXTRecord, error) {
	records, err := solver.client.ListTXTRecords(ctx, zone.ID, intent.FQDN)
	if err != nil {
		return nil, err
	}
	matches := make([]TXTRecord, 0, 1)
	for _, record := range records {
		if record.Name == intent.FQDN && acmeflow.HashChallengeValue(record.Content) == intent.ValueHash {
			matches = append(matches, record)
		}
	}
	return matches, nil
}

func (solver *DNS01Solver) completeIntent(ctx context.Context, id string) error {
	if err := solver.intents.CompleteChallengeIntent(ctx, id); err != nil && !errors.Is(err, acmeflow.ErrChallengeIntentNotFound) {
		return providerError(acmeflow.CategoryCleanup, "dns01_cleanup", err)
	}
	return nil
}

func (solver *DNS01Solver) findIntent(ctx context.Context, id string) (*acmeflow.ChallengeIntent, error) {
	intents, err := solver.intents.ListChallengeIntents(ctx)
	if err != nil {
		return nil, err
	}
	for _, intent := range intents {
		if intent.ID == id {
			clone := intent
			return &clone, nil
		}
	}
	return nil, nil
}

func (solver *DNS01Solver) session(challenge acmeflow.Challenge) (dns01Session, bool) {
	solver.sessionsMu.Lock()
	defer solver.sessionsMu.Unlock()
	session, exists := solver.sessions[dns01ChallengeKey(challenge)]
	return session, exists
}

func (solver *DNS01Solver) setSession(challenge acmeflow.Challenge, session dns01Session) {
	solver.sessionsMu.Lock()
	defer solver.sessionsMu.Unlock()
	solver.sessions[dns01ChallengeKey(challenge)] = session
}

func (solver *DNS01Solver) deleteSession(challenge acmeflow.Challenge) {
	solver.sessionsMu.Lock()
	defer solver.sessionsMu.Unlock()
	delete(solver.sessions, dns01ChallengeKey(challenge))
}

func dns01ChallengeName(challenge acmeflow.Challenge) (string, error) {
	if challenge.Type != acmeflow.ChallengeDNS01 || challenge.Identifier.Type != acmeflow.IdentifierDNS || challenge.DNSValue == "" || len(challenge.DNSValue) > 4096 || strings.ContainsRune(challenge.DNSValue, '\x00') {
		return "", errors.New("DNS-01 challenge is invalid")
	}
	identifier := strings.TrimSpace(challenge.Identifier.Value)
	identifier = strings.TrimPrefix(identifier, "*.")
	identifier, err := normalizeDNSName(identifier)
	if err != nil {
		return "", errors.New("DNS-01 identifier is invalid")
	}
	return normalizeDNSName("_acme-challenge." + identifier)
}

func dns01ChallengeKey(challenge acmeflow.Challenge) string {
	hash := sha256.Sum256([]byte(string(challenge.Identifier.Type) + "\x00" + challenge.Identifier.Value + "\x00" + challenge.DNSValue))
	return hex.EncodeToString(hash[:])
}
