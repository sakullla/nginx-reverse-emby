package generation

import (
	"context"
	"errors"
	"sync"
)

type EntityKey struct {
	Module string
	ID     string
}
type Session interface {
	ForceClose(context.Context, string) error
}

type SessionHandle struct {
	once   sync.Once
	finish func()
}

func (h *SessionHandle) Finish() {
	if h != nil && h.claim() {
		h.finish()
	}
}

func (h *SessionHandle) claim() bool {
	claimed := false
	h.once.Do(func() { claimed = true })
	return claimed
}

type sessionRecord struct {
	generation string
	entity     EntityKey
	id         string
	session    Session
	handle     *SessionHandle
}
type SessionRegistry struct {
	mu          sync.Mutex
	generations map[string]map[EntityKey]map[string]*sessionRecord
	onEmpty     func(string)
}

func NewSessionRegistry(onEmpty func(string)) *SessionRegistry {
	return &SessionRegistry{generations: make(map[string]map[EntityKey]map[string]*sessionRecord), onEmpty: onEmpty}
}

func (r *SessionRegistry) Register(generation string, entity EntityKey, id string, session Session) (*SessionHandle, error) {
	if r == nil || generation == "" || entity.Module == "" || entity.ID == "" || id == "" || session == nil {
		return nil, errors.New("invalid session registration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entities := r.generations[generation]
	if entities == nil {
		entities = make(map[EntityKey]map[string]*sessionRecord)
		r.generations[generation] = entities
	}
	sessions := entities[entity]
	if sessions == nil {
		sessions = make(map[string]*sessionRecord)
		entities[entity] = sessions
	}
	if _, ok := sessions[id]; ok {
		return nil, errors.New("duplicate session registration")
	}
	h := &SessionHandle{}
	rec := &sessionRecord{generation: generation, entity: entity, id: id, session: session, handle: h}
	h.finish = func() { r.finish(rec) }
	sessions[id] = rec
	return h, nil
}

func (r *SessionRegistry) finish(rec *sessionRecord) {
	if r == nil || rec == nil {
		return
	}
	empty := false
	r.mu.Lock()
	if entities := r.generations[rec.generation]; entities != nil {
		if sessions := entities[rec.entity]; sessions != nil {
			if sessions[rec.id] == rec {
				delete(sessions, rec.id)
			}
			if len(sessions) == 0 {
				delete(entities, rec.entity)
			}
		}
		if len(entities) == 0 {
			delete(r.generations, rec.generation)
			empty = true
		}
	}
	r.mu.Unlock()
	if empty && r.onEmpty != nil {
		r.onEmpty(rec.generation)
	}
}

func (r *SessionRegistry) GenerationCount(generation string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, sessions := range r.generations[generation] {
		n += len(sessions)
	}
	return n
}

func (r *SessionRegistry) ForceEntities(ctx context.Context, generation string, entities map[EntityKey]string) (int, error) {
	return r.force(ctx, generation, func(k EntityKey) (string, bool) { reason, ok := entities[k]; return reason, ok })
}
func (r *SessionRegistry) ForceGeneration(ctx context.Context, generation, reason string) (int, error) {
	return r.force(ctx, generation, func(EntityKey) (string, bool) { return reason, true })
}
func (r *SessionRegistry) force(ctx context.Context, generation string, selectReason func(EntityKey) (string, bool)) (int, error) {
	if r == nil {
		return 0, nil
	}
	var records []*sessionRecord
	r.mu.Lock()
	for entity, sessions := range r.generations[generation] {
		if _, ok := selectReason(entity); !ok {
			continue
		}
		for _, rec := range sessions {
			records = append(records, rec)
		}
	}
	r.mu.Unlock()
	var closeErr error
	forced := 0
	for _, rec := range records {
		if !rec.handle.claim() {
			continue
		}
		reason, _ := selectReason(rec.entity)
		closeErr = errors.Join(closeErr, rec.session.ForceClose(ctx, reason))
		r.finish(rec)
		forced++
	}
	return forced, closeErr
}
