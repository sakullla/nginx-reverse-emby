package module

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type ProviderRef string

type Capability struct {
	Name     string
	Enabled  bool
	Metadata map[string]string
}

type Health struct {
	Status  string
	Message string
}

type ModuleDescriptor struct {
	Name     string
	Provides []ProviderRef
	Requires []ProviderRef
	Optional []ProviderRef
}

type SnapshotView = model.Snapshot

type ApplyRequest struct {
	Previous  model.Snapshot
	Next      model.Snapshot
	Providers ProviderResolver
}

type ProviderRegistry interface {
	Provide(ProviderRef, any) error
}

type ProviderResolver interface {
	Resolve(ProviderRef) (any, bool)
}

type Module interface {
	Name() string
	Descriptor() ModuleDescriptor
	RegisterProviders(ProviderRegistry) error
	Capabilities(SnapshotView) []Capability
	Apply(context.Context, ApplyRequest) error
	Stop(context.Context) error
}

type TransactionalModule interface {
	Module
	Prepare(context.Context, ApplyRequest) (ModuleTransaction, error)
}

type ModuleTransaction interface {
	Commit() error
	Rollback() error
}

type TransactionFuncs struct {
	CommitFunc   func() error
	RollbackFunc func() error
}

func (f TransactionFuncs) Commit() error {
	if f.CommitFunc == nil {
		return nil
	}
	return f.CommitFunc()
}

func (f TransactionFuncs) Rollback() error {
	if f.RollbackFunc == nil {
		return nil
	}
	return f.RollbackFunc()
}
