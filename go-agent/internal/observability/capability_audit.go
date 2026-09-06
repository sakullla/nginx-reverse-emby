package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/hostapi"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const (
	maxCapabilityAuditBytes       int64 = 4 << 20
	maxCapabilityAuditArchives          = 3
	maxCapabilityAuditRecordBytes       = 4 << 10
)

var capabilityAuditIdentityPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,255}$`)

type CapabilityAuditJournal struct {
	mu            sync.Mutex
	file          *os.File
	path          string
	maxBytes      int64
	maxTotalBytes int64
	archives      int
	retention     time.Duration
	size          int64
	lastActivity  time.Time
	closed        bool
}

type capabilityAuditRecord struct {
	Timestamp       time.Time `json:"timestamp"`
	PluginID        string    `json:"plugin_id"`
	InstanceID      string    `json:"instance_id"`
	Generation      string    `json:"generation"`
	Capability      string    `json:"capability"`
	ActorID         string    `json:"actor_id"`
	ResourceGroupID string    `json:"resource_group_id"`
	TargetKind      string    `json:"target_kind"`
	TargetID        string    `json:"target_id"`
	Outcome         string    `json:"outcome"`
	Reason          string    `json:"reason,omitempty"`
}

func NewCapabilityAuditJournal(path string) (*CapabilityAuditJournal, error) {
	return newCapabilityAuditJournalWithLimits(path, maxCapabilityAuditBytes, model.DefaultCapabilityAuditMaxBytes, maxCapabilityAuditArchives, model.DefaultCapabilityAuditRetention, time.Now().UTC())
}

func newCapabilityAuditJournal(path string, maxBytes int64, archives int) (*CapabilityAuditJournal, error) {
	return newCapabilityAuditJournalWithLimits(path, maxBytes, maxBytes*int64(archives+1), archives, model.DefaultCapabilityAuditRetention, time.Now().UTC())
}

func newCapabilityAuditJournalWithLimits(path string, maxBytes, maxTotalBytes int64, archives int, retention time.Duration, now time.Time) (*CapabilityAuditJournal, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, errors.New("capability audit path must be absolute")
	}
	if maxBytes <= 0 || maxTotalBytes < maxBytes || archives <= 0 || retention <= 0 {
		return nil, errors.New("capability audit retention limits are invalid")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create capability audit directory: %w", err)
	}
	if err := recoverCapabilityAudit(path, maxBytes, archives); err != nil {
		return nil, err
	}
	if err := cleanupCapabilityAuditFiles(path, archives, retention, maxTotalBytes, now, true); err != nil {
		return nil, err
	}
	file, size, modified, err := openCapabilityAuditFile(path)
	if err != nil {
		return nil, fmt.Errorf("open capability audit journal: %w", err)
	}
	return &CapabilityAuditJournal{file: file, path: path, maxBytes: maxBytes, maxTotalBytes: maxTotalBytes, archives: archives, retention: retention, size: size, lastActivity: modified}, nil
}

func (journal *CapabilityAuditJournal) Audit(_ context.Context, event hostapi.AuditEvent) error {
	return journal.writeBatch([]hostapi.AuditEvent{event}, time.Now().UTC())
}

func (journal *CapabilityAuditJournal) writeBatch(events []hostapi.AuditEvent, now time.Time) error {
	encoded, _, err := encodeCapabilityAuditBatch(events, now)
	if err != nil {
		return err
	}
	return journal.writeEncodedBatch(encoded, now)
}

func (journal *CapabilityAuditJournal) writeEncodedBatch(encoded []byte, now time.Time) error {
	if journal == nil {
		return errors.New("capability audit journal is unavailable")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed || journal.file == nil {
		return errors.New("capability audit journal is closed")
	}
	if err := journal.maintainLocked(now); err != nil {
		return err
	}
	if len(encoded) == 0 {
		return nil
	}
	if int64(len(encoded)) > journal.maxBytes {
		return errors.New("capability audit batch exceeds the active file bound")
	}
	if journal.size+int64(len(encoded)) > journal.maxBytes {
		if err := journal.rotateLocked(now); err != nil {
			return fmt.Errorf("rotate capability audit journal: %w", err)
		}
	}
	start := journal.size
	written, writeErr := journal.file.Write(encoded)
	if writeErr != nil || written != len(encoded) {
		rollbackErr := journal.file.Truncate(start)
		journal.size = start
		if rollbackErr == nil {
			rollbackErr = journal.file.Sync()
		}
		if writeErr == nil {
			writeErr = errors.New("capability audit journal short write")
		}
		return errors.Join(writeErr, rollbackErr)
	}
	journal.size += int64(written)
	journal.lastActivity = now
	if err := journal.maintainLocked(now); err != nil {
		return err
	}
	return journal.file.Sync()
}

func encodeCapabilityAuditBatch(events []hostapi.AuditEvent, now time.Time) ([]byte, uint64, error) {
	var batch bytes.Buffer
	allocation := uint64(0)
	maxInt := uint64(^uint(0) >> 1)
	for _, event := range events {
		encoded, err := encodeCapabilityAuditRecord(event, now)
		if err != nil {
			return nil, 0, err
		}
		if len(encoded) > maxCapabilityAuditRecordBytes {
			return nil, 0, errors.New("capability audit record exceeds its bound")
		}
		recordBytes := uint64(len(encoded))
		if recordBytes > math.MaxUint64-allocation || allocation+recordBytes > maxInt {
			return nil, 0, errors.New("capability audit batch allocation overflows")
		}
		allocation += recordBytes
		_, _ = batch.Write(encoded)
	}
	return batch.Bytes(), allocation, nil
}

func encodeCapabilityAuditRecord(event hostapi.AuditEvent, now time.Time) ([]byte, error) {
	record := capabilityAuditRecord{
		Timestamp: now.UTC(), PluginID: canonicalAuditIdentity(event.Call.PluginID),
		InstanceID: canonicalAuditIdentity(event.Call.InstanceID), Generation: canonicalAuditIdentity(event.Call.Generation),
		Capability: canonicalAuditCapability(event.Call.Capability), ActorID: canonicalAuditIdentity(event.Call.Actor.ID),
		ResourceGroupID: canonicalAuditIdentity(event.Call.Target.ResourceGroupID), TargetKind: canonicalAuditIdentity(event.Call.Target.Kind),
		TargetID: canonicalAuditIdentity(event.Call.Target.ID), Outcome: canonicalAuditOutcome(event.Outcome), Reason: canonicalAuditReason(event.Reason),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func (journal *CapabilityAuditJournal) maintainLocked(now time.Time) error {
	if journal.file != nil {
		if journal.size > 0 && journal.lastActivity.Before(now.Add(-journal.retention)) {
			return journal.rotateLocked(now)
		}
	}
	return cleanupCapabilityAuditFiles(journal.path, journal.archives, journal.retention, journal.maxTotalBytes, now, false)
}

func (journal *CapabilityAuditJournal) maintain(now time.Time) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return nil
	}
	return journal.maintainLocked(now)
}

func (journal *CapabilityAuditJournal) rotateLocked(now time.Time) error {
	if journal.file == nil {
		return errors.New("capability audit journal is unavailable")
	}
	if err := journal.file.Sync(); err != nil {
		return err
	}
	if err := journal.file.Close(); err != nil {
		return err
	}
	journal.file = nil
	journal.size = 0
	if err := rotateCapabilityAuditFiles(journal.path, journal.archives); err != nil {
		return err
	}
	if err := cleanupCapabilityAuditFiles(journal.path, journal.archives, journal.retention, journal.maxTotalBytes, now, false); err != nil {
		return err
	}
	file, size, modified, err := openCapabilityAuditFile(journal.path)
	if err != nil {
		return err
	}
	journal.file, journal.size, journal.lastActivity = file, size, modified
	return nil
}

func openCapabilityAuditFile(path string) (*os.File, int64, time.Time, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		if err := createCapabilityAuditFile(path); err != nil {
			return nil, 0, time.Time{}, err
		}
		file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	}
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, time.Time{}, err
	}
	return file, info.Size(), info.ModTime(), nil
}

func createCapabilityAuditFile(path string) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".create-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := durableAuditCreate(temporaryPath, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		return nil
	}
	return syncAuditDirectory(filepath.Dir(path))
}

func recoverCapabilityAudit(path string, maxBytes int64, archives int) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("recover capability audit journal: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	complete, err := lastCompleteAuditSize(file, info.Size())
	if err != nil {
		_ = file.Close()
		return err
	}
	if complete != info.Size() {
		if err := file.Truncate(complete); err != nil {
			_ = file.Close()
			return fmt.Errorf("truncate unacknowledged capability audit tail: %w", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	if complete >= maxBytes {
		if err := rotateCapabilityAuditFiles(path, archives); err != nil {
			return fmt.Errorf("rotate recovered capability audit journal: %w", err)
		}
	}
	return syncAuditDirectory(filepath.Dir(path))
}

func lastCompleteAuditSize(file *os.File, size int64) (int64, error) {
	if size == 0 {
		return 0, nil
	}
	buffer := make([]byte, 4<<10)
	start := size - int64(len(buffer))
	if start < 0 {
		start = 0
	}
	chunk := buffer[:size-start]
	read, err := file.ReadAt(chunk, start)
	if err != nil && read != len(chunk) {
		return 0, err
	}
	if index := bytes.LastIndexByte(chunk[:read], '\n'); index >= 0 {
		return start + int64(index) + 1, nil
	}
	// A valid record is always bounded to this window. If a legacy partial
	// tail exceeds it, discard the file rather than performing unbounded
	// startup I/O to search for an earlier delimiter.
	return 0, nil
}

type capabilityAuditOwnedFile struct {
	path string
	info os.FileInfo
}

func cleanupCapabilityAuditFiles(path string, archives int, retention time.Duration, maxTotalBytes int64, now time.Time, includeActive bool) error {
	owned := make([]capabilityAuditOwnedFile, 0, archives+1)
	removed := false
	for index := 0; index <= archives; index++ {
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		info, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("capability audit path %s is not a regular file", candidate)
		}
		if (includeActive || index > 0) && info.ModTime().Before(now.Add(-retention)) {
			if err := os.Remove(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			removed = true
			continue
		}
		owned = append(owned, capabilityAuditOwnedFile{path: candidate, info: info})
	}
	total := int64(0)
	for _, file := range owned {
		total += file.info.Size()
	}
	if total <= maxTotalBytes {
		if removed {
			return syncAuditDirectory(filepath.Dir(path))
		}
		return nil
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].info.ModTime().Before(owned[j].info.ModTime()) })
	for _, file := range owned {
		if total <= maxTotalBytes {
			break
		}
		if !includeActive && file.path == path {
			continue
		}
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		removed = true
		total -= file.info.Size()
	}
	if removed {
		return syncAuditDirectory(filepath.Dir(path))
	}
	return nil
}

func rotateCapabilityAuditFiles(path string, archives int) error {
	oldest := fmt.Sprintf("%s.%d", path, archives)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for index := archives - 1; index >= 1; index-- {
		from, to := fmt.Sprintf("%s.%d", path, index), fmt.Sprintf("%s.%d", path, index+1)
		if err := durableAuditRename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := durableAuditRename(path, path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncAuditDirectory(filepath.Dir(path))
}

func (journal *CapabilityAuditJournal) Close() error {
	if journal == nil {
		return nil
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return nil
	}
	journal.closed = true
	if journal.file == nil {
		return nil
	}
	err := journal.file.Close()
	journal.file = nil
	return err
}

type CapabilityAuditStatus struct {
	Enabled     bool      `json:"enabled"`
	Queued      int       `json:"queued"`
	Dropped     uint64    `json:"dropped"`
	WriteErrors uint64    `json:"write_errors"`
	LowSpace    bool      `json:"low_space"`
	LastError   string    `json:"last_error,omitempty"`
	LastFlush   time.Time `json:"last_flush,omitempty"`
}

type capabilityAuditBatchWriter interface {
	writeEncodedBatch([]byte, time.Time) error
	maintain(time.Time) error
	Close() error
}

type capabilityAuditAsyncOptions struct {
	now       func() time.Time
	freeSpace func(string) (uint64, error)
	open      func(string, model.CapabilityAuditConfig, time.Time) (capabilityAuditBatchWriter, error)
}

type AsyncCapabilityAuditor struct {
	path        string
	config      model.CapabilityAuditConfig
	queue       chan hostapi.AuditEvent
	stop        chan struct{}
	done        chan struct{}
	once        sync.Once
	enqueueMu   sync.RWMutex
	closed      atomic.Bool
	queued      atomic.Int64
	dropped     atomic.Uint64
	writeErrors atomic.Uint64
	lowSpace    atomic.Bool
	statusMu    sync.RWMutex
	lastError   string
	lastFlush   time.Time
	options     capabilityAuditAsyncOptions
}

func NewAsyncCapabilityAuditor(path string, cfg model.CapabilityAuditConfig) (*AsyncCapabilityAuditor, error) {
	return newAsyncCapabilityAuditor(path, cfg, capabilityAuditAsyncOptions{})
}

func newAsyncCapabilityAuditor(path string, cfg model.CapabilityAuditConfig, options capabilityAuditAsyncOptions) (*AsyncCapabilityAuditor, error) {
	cfg = model.NormalizeCapabilityAuditConfig(cfg)
	if !cfg.Enabled {
		return nil, errors.New("capability audit must be enabled before creating its runtime")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, errors.New("capability audit path must be absolute")
	}
	if options.now == nil {
		options.now = func() time.Time { return time.Now().UTC() }
	}
	if options.freeSpace == nil {
		options.freeSpace = capabilityAuditFreeSpace
	}
	if options.open == nil {
		options.open = func(path string, cfg model.CapabilityAuditConfig, now time.Time) (capabilityAuditBatchWriter, error) {
			return newCapabilityAuditJournalWithLimits(path, cfg.MaxBytes/int64(maxCapabilityAuditArchives+1), cfg.MaxBytes, maxCapabilityAuditArchives, cfg.Retention, now)
		}
	}
	auditor := &AsyncCapabilityAuditor{path: path, config: cfg, queue: make(chan hostapi.AuditEvent, cfg.QueueSize), stop: make(chan struct{}), done: make(chan struct{}), options: options}
	go auditor.run()
	return auditor, nil
}

func (auditor *AsyncCapabilityAuditor) Audit(_ context.Context, event hostapi.AuditEvent) error {
	if auditor == nil {
		return nil
	}
	auditor.enqueueMu.RLock()
	defer auditor.enqueueMu.RUnlock()
	if auditor.closed.Load() {
		saturatingIncrement(&auditor.dropped, 1)
		return nil
	}
	event = boundedCapabilityAuditEvent(event)
	auditor.queued.Add(1)
	select {
	case auditor.queue <- event:
	default:
		auditor.queued.Add(-1)
		saturatingIncrement(&auditor.dropped, 1)
	}
	return nil
}

func boundedCapabilityAuditEvent(event hostapi.AuditEvent) hostapi.AuditEvent {
	return hostapi.AuditEvent{Call: pluginsdk.HostCapabilityCall{
		PluginID: canonicalAuditIdentity(event.Call.PluginID), InstanceID: canonicalAuditIdentity(event.Call.InstanceID),
		Generation: canonicalAuditIdentity(event.Call.Generation), Capability: pluginsdk.HostCapability(canonicalAuditCapability(event.Call.Capability)),
		Actor:  pluginsdk.HostActor{ID: canonicalAuditIdentity(event.Call.Actor.ID), ResourceGroupID: canonicalAuditIdentity(event.Call.Actor.ResourceGroupID)},
		Target: pluginsdk.HostTarget{Kind: canonicalAuditIdentity(event.Call.Target.Kind), ID: canonicalAuditIdentity(event.Call.Target.ID), ResourceGroupID: canonicalAuditIdentity(event.Call.Target.ResourceGroupID)},
	}, Outcome: canonicalAuditOutcome(event.Outcome), Reason: canonicalAuditReason(event.Reason)}
}

func (auditor *AsyncCapabilityAuditor) run() {
	defer close(auditor.done)
	ticker := time.NewTicker(auditor.config.FlushInterval)
	defer ticker.Stop()
	var writer capabilityAuditBatchWriter
	batch := make([]hostapi.AuditEvent, 0, auditor.config.BatchSize)
	flush := func(maintainOnly bool) {
		now := auditor.options.now().UTC()
		if writer == nil {
			opened, err := auditor.options.open(auditor.path, auditor.config, now)
			if err != nil {
				auditor.recordWriteFailure(err, len(batch))
				auditor.queued.Add(-int64(len(batch)))
				batch = batch[:0]
				return
			}
			writer = opened
		}
		if err := writer.maintain(now); err != nil {
			auditor.recordWriteFailure(err, len(batch))
			auditor.queued.Add(-int64(len(batch)))
			batch = batch[:0]
			return
		}
		var encoded []byte
		allocation := uint64(0)
		if len(batch) > 0 {
			var err error
			encoded, allocation, err = encodeCapabilityAuditBatch(batch, now)
			if err != nil || allocation > uint64(auditor.config.MaxBytes/int64(maxCapabilityAuditArchives+1)) {
				if err == nil {
					err = errors.New("capability audit batch exceeds the active file allocation")
				}
				auditor.recordWriteFailure(err, len(batch))
				auditor.queued.Add(-int64(len(batch)))
				batch = batch[:0]
				return
			}
		}
		free, err := auditor.options.freeSpace(filepath.Dir(auditor.path))
		if err != nil || !capabilityAuditSpaceAvailable(free, auditor.config.MinFreeBytes, allocation) {
			auditor.lowSpace.Store(true)
			if err == nil {
				err = fmt.Errorf("capability audit free space %d cannot preserve minimum %d after allocating %d bytes", free, auditor.config.MinFreeBytes, allocation)
			}
			auditor.recordWriteFailure(err, len(batch))
			auditor.queued.Add(-int64(len(batch)))
			batch = batch[:0]
			return
		}
		auditor.lowSpace.Store(false)
		if maintainOnly || len(batch) == 0 {
			return
		}
		if err := writer.writeEncodedBatch(encoded, now); err != nil {
			auditor.recordWriteFailure(err, len(batch))
			auditor.queued.Add(-int64(len(batch)))
			_ = writer.Close()
			writer = nil
			batch = batch[:0]
			return
		}
		auditor.statusMu.Lock()
		auditor.lastError = ""
		auditor.lastFlush = now
		auditor.statusMu.Unlock()
		auditor.queued.Add(-int64(len(batch)))
		batch = batch[:0]
	}
	flush(true)
	for {
		select {
		case event := <-auditor.queue:
			batch = append(batch, event)
			if len(batch) >= auditor.config.BatchSize {
				flush(false)
			}
		case <-ticker.C:
			flush(len(batch) == 0)
		case <-auditor.stop:
			for {
				select {
				case event := <-auditor.queue:
					batch = append(batch, event)
					if len(batch) >= auditor.config.BatchSize {
						flush(false)
					}
				default:
					flush(false)
					if writer != nil {
						if err := writer.Close(); err != nil {
							auditor.recordWriteFailure(err, 0)
						}
					}
					return
				}
			}
		}
	}
}

func capabilityAuditSpaceAvailable(free, minimum, allocation uint64) bool {
	if allocation > free || minimum > math.MaxUint64-allocation {
		return false
	}
	return free >= minimum+allocation
}

func (auditor *AsyncCapabilityAuditor) recordWriteFailure(err error, dropped int) {
	if dropped > 0 {
		saturatingIncrement(&auditor.dropped, uint64(dropped))
	}
	saturatingIncrement(&auditor.writeErrors, 1)
	auditor.statusMu.Lock()
	auditor.lastError = boundedCapabilityAuditError(err)
	auditor.statusMu.Unlock()
}

func (auditor *AsyncCapabilityAuditor) Status() CapabilityAuditStatus {
	if auditor == nil {
		return CapabilityAuditStatus{}
	}
	auditor.statusMu.RLock()
	queued := auditor.queued.Load()
	if queued < 0 {
		queued = 0
	}
	status := CapabilityAuditStatus{Enabled: true, Queued: int(queued), Dropped: auditor.dropped.Load(), WriteErrors: auditor.writeErrors.Load(), LowSpace: auditor.lowSpace.Load(), LastError: auditor.lastError, LastFlush: auditor.lastFlush}
	auditor.statusMu.RUnlock()
	return status
}

func (auditor *AsyncCapabilityAuditor) Close() error {
	if auditor == nil {
		return nil
	}
	auditor.enqueueMu.Lock()
	auditor.closed.Store(true)
	auditor.once.Do(func() { close(auditor.stop) })
	auditor.enqueueMu.Unlock()
	timer := time.NewTimer(auditor.config.CloseTimeout)
	defer timer.Stop()
	select {
	case <-auditor.done:
		return nil
	case <-timer.C:
		return errors.New("capability audit close timed out")
	}
}

func saturatingIncrement(counter *atomic.Uint64, delta uint64) {
	for {
		current := counter.Load()
		if current == math.MaxUint64 {
			return
		}
		next := current + delta
		if next < current {
			next = math.MaxUint64
		}
		if counter.CompareAndSwap(current, next) {
			return
		}
	}
}

func boundedCapabilityAuditError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(err.Error())
	if len(value) > 256 {
		value = value[:256]
	}
	return value
}

func canonicalAuditIdentity(value string) string {
	if len(value) > 256 || pluginsdk.ValidatePolicyIdentity(value) != nil || !capabilityAuditIdentityPattern.MatchString(value) {
		return "invalid"
	}
	return value
}

func canonicalAuditCapability(value pluginsdk.HostCapability) string {
	if len(value) > 256 || value.Validate() != nil {
		return "invalid"
	}
	return string(value)
}

func canonicalAuditOutcome(value string) string {
	if value == "allowed" || value == "denied" {
		return value
	}
	return "invalid"
}

func canonicalAuditReason(value string) string {
	switch value {
	case "", "invalid_call", "owner_denied", "not_declared", "not_granted", "actor_denied", "target_denied", "quota_unavailable", "quota_denied", "audit_unavailable":
		return value
	default:
		return "invalid"
	}
}

type CapabilityAuditObserver struct {
	Observer Observer
	Auditor  hostapi.Auditor
}

func (observer CapabilityAuditObserver) Observe(ctx context.Context, event Event) {
	if observer.Observer != nil {
		observer.Observer.Observe(ctx, event)
	}
}

func (observer CapabilityAuditObserver) Audit(ctx context.Context, event hostapi.AuditEvent) error {
	if observer.Auditor != nil {
		_ = observer.Auditor.Audit(ctx, event)
	}
	return nil
}
