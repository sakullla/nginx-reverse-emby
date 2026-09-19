package policy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const ProviderDatasets module.ProviderRef = "datasets.index"

type datasetCachedIndex struct {
	index  *datasets.Index
	users  int
	memory int64
}
type datasetIndexPool struct {
	mu     sync.Mutex
	values map[string]*datasetCachedIndex
	memory int64
}

// DatasetGeneration belongs to the same atomic GenerationView as the rules.
// Open/Query authorization must be supplied from the actual Host invocation,
// not JSON. The caller's signed and live granted scopes are rechecked per use;
// references are random, exact-record matched, and revoked with this generation.
type DatasetGeneration struct {
	mu                  sync.RWMutex
	closed              bool
	generation          string
	indices             map[string]*datasets.Index
	bindings            map[string]map[string]map[string]bool
	instanceGenerations map[string]string
	handles             map[string]pluginsdk.DatasetReference
	pool                *datasetIndexPool
	poolKeys            []string
}

func datasetSelectorKey(value pluginsdk.DatasetClassification) string {
	value.Attributes = slices.Clone(value.Attributes)
	slices.SortFunc(value.Attributes, func(a, b pluginsdk.DatasetAttribute) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func prepareDatasetGeneration(ctx context.Context, generation string, snapshot model.Snapshot, pool *datasetIndexPool) (*DatasetGeneration, error) {
	provider := &DatasetGeneration{generation: generation, indices: map[string]*datasets.Index{}, bindings: map[string]map[string]map[string]bool{}, instanceGenerations: map[string]string{}, handles: map[string]pluginsdk.DatasetReference{}, pool: pool}
	for _, instance := range snapshot.PluginGenerations {
		provider.instanceGenerations[instance.InstanceID] = generation
	}
	for _, policy := range snapshot.PluginPolicies {
		for _, stage := range policy.Stages {
			if provider.instanceGenerations[stage.InstanceID] == "" {
				provider.instanceGenerations[stage.InstanceID] = generation
			}
		}
	}
	ok := false
	defer func() {
		if !ok {
			provider.Close()
		}
	}()
	for _, entry := range snapshot.Datasets {
		if err := entry.Validate(); err != nil {
			return nil, err
		}
		if provider.indices[entry.Version.SourceID] != nil {
			return nil, errors.New("duplicate dataset source in generation")
		}
		index, err := pool.acquire(ctx, entry)
		if err != nil {
			return nil, err
		}
		provider.poolKeys = append(provider.poolKeys, entry.Artifact.SHA256)
		provider.indices[entry.Version.SourceID] = index
		for _, binding := range entry.Bindings {
			if provider.instanceGenerations[binding.InstanceID] == "" {
				return nil, errors.New("dataset binding refers to an absent plugin instance")
			}
			if err := validateDatasetClassifications(ctx, index, binding.Classifications); err != nil {
				return nil, err
			}
			if provider.bindings[binding.InstanceID] == nil {
				provider.bindings[binding.InstanceID] = map[string]map[string]bool{}
			}
			allowed := map[string]bool{}
			for _, selector := range binding.Classifications {
				allowed[datasetSelectorKey(selector)] = true
			}
			provider.bindings[binding.InstanceID][entry.Version.SourceID] = allowed
		}
	}
	ok = true
	return provider, nil
}

func (pool *datasetIndexPool) acquire(ctx context.Context, entry model.DatasetSnapshot) (*datasets.Index, error) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.values == nil {
		pool.values = map[string]*datasetCachedIndex{}
	}
	if cached := pool.values[entry.Artifact.SHA256]; cached != nil {
		if cached.index.Version() != entry.Version {
			return nil, errors.New("dataset manifest differs from cached index")
		}
		cached.users++
		return cached.index, nil
	}
	if entry.Artifact.LocalPath == "" {
		return nil, errors.New("dataset index is not materialized")
	}
	remaining := pluginsdk.DatasetDefaultIndexBudgetBytes - pool.memory
	if entry.Artifact.SizeBytes > remaining {
		return nil, &datasets.Error{Code: pluginsdk.DatasetFailureBudget, Detail: "candidate exceeds remaining node index budget"}
	}
	info, err := os.Lstat(entry.Artifact.LocalPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != entry.Artifact.SizeBytes {
		return nil, errors.New("dataset index file is unavailable or invalid")
	}
	file, err := os.Open(entry.Artifact.LocalPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, entry.Artifact.SizeBytes+1))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	if int64(len(encoded)) != entry.Artifact.SizeBytes || hex.EncodeToString(digest[:]) != entry.Artifact.SHA256 {
		return nil, errors.New("dataset artifact digest mismatch")
	}
	limits := datasets.DefaultLimits()
	limits.MaxMemoryBytes = remaining
	index, err := datasets.LoadIndex(ctx, encoded, limits)
	if err != nil {
		return nil, err
	}
	if index.Version() != entry.Version {
		return nil, errors.New("dataset index manifest or digest mismatch")
	}
	memory := index.Stats().EstimatedMemoryBytes
	if memory <= 0 || pool.memory+memory > pluginsdk.DatasetDefaultIndexBudgetBytes {
		return nil, &datasets.Error{Code: pluginsdk.DatasetFailureBudget, Detail: "all retained dataset generations exceed node memory budget"}
	}
	pool.values[entry.Artifact.SHA256] = &datasetCachedIndex{index: index, users: 1, memory: memory}
	pool.memory += memory
	return index, nil
}

func validateDatasetClassifications(ctx context.Context, index *datasets.Index, selectors []pluginsdk.DatasetClassification) error {
	wanted := map[string]bool{}
	for _, selector := range selectors {
		wanted[string(selector.Kind)+":"+selector.Name] = true
	}
	request := pluginsdk.DatasetCatalogRequest{SourceID: index.Version().SourceID, VersionDigest: index.Version().Digest, Limit: pluginsdk.DatasetMaxCatalogPage}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := index.Catalog(request)
		if err != nil {
			return err
		}
		for _, entry := range page.Classifications {
			delete(wanted, string(entry.Classification.Kind)+":"+entry.Classification.Name)
		}
		if len(wanted) == 0 {
			return nil
		}
		if page.NextCursor == "" {
			return &datasets.Error{Code: pluginsdk.DatasetFailureMissingClassification, Detail: "a bound dataset classification disappeared"}
		}
		request.Cursor = page.NextCursor
	}
}

func datasetAuthorizationAllowed(authorization pluginsdk.PolicyHostCallAuthorization) bool {
	if authorization.Validate() != nil {
		return false
	}
	declared, granted := false, false
	for _, scope := range authorization.DeclaredScopes {
		if scope == string(pluginsdk.CapabilityDatasetQuery) {
			declared = true
		}
	}
	for _, scope := range authorization.GrantedScopes {
		if scope == string(pluginsdk.CapabilityDatasetQuery) {
			granted = true
		}
	}
	return declared && granted
}

// ResolveDataset implements the public source-only contract using this view's
// immutable binding, including connectionless initialization. The SDK boundary
// authenticates/grants the caller before invoking this method.
func (provider *DatasetGeneration) ResolveDataset(ctx context.Context, binding pluginsdk.DatasetResolveBinding, request pluginsdk.DatasetResolveRequest) (pluginsdk.DatasetReference, error) {
	if err := ctx.Err(); err != nil {
		return pluginsdk.DatasetReference{}, err
	}
	if provider == nil || binding.Validate() != nil || request.Validate() != nil {
		return pluginsdk.DatasetReference{}, errors.New("dataset resolve authority unavailable")
	}
	provider.mu.RLock()
	index := provider.indices[request.SourceID]
	valid := !provider.closed && provider.instanceGenerations[binding.InstanceID] == binding.Generation && len(provider.bindings[binding.InstanceID][request.SourceID]) > 0
	provider.mu.RUnlock()
	if !valid || index == nil {
		return pluginsdk.DatasetReference{}, &pluginsdk.RuntimeError{Code: pluginsdk.ErrorUnavailable, Message: "dataset source is not bound to runtime generation"}
	}
	return provider.openBound(binding, pluginsdk.DatasetOpenRequest{SourceID: request.SourceID, VersionDigest: index.Version().Digest})
}

func (provider *DatasetGeneration) Open(authorization pluginsdk.PolicyHostCallAuthorization, request pluginsdk.DatasetOpenRequest) (pluginsdk.DatasetReference, error) {
	if provider == nil || !datasetAuthorizationAllowed(authorization) {
		return pluginsdk.DatasetReference{}, &pluginsdk.RuntimeError{Code: pluginsdk.ErrorPermissionDenied, Message: "dataset authorization denied"}
	}
	if err := request.Validate(); err != nil {
		return pluginsdk.DatasetReference{}, err
	}
	return provider.openBound(pluginsdk.DatasetResolveBinding{InstanceID: authorization.InstanceID, Generation: authorization.Generation}, request)
}

func (provider *DatasetGeneration) openBound(binding pluginsdk.DatasetResolveBinding, request pluginsdk.DatasetOpenRequest) (pluginsdk.DatasetReference, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.closed || provider.instanceGenerations[binding.InstanceID] != binding.Generation {
		return pluginsdk.DatasetReference{}, &pluginsdk.RuntimeError{Code: pluginsdk.ErrorUnavailable, Message: "dataset generation is stale"}
	}
	index := provider.indices[request.SourceID]
	if index == nil || index.Version().Digest != request.VersionDigest || len(provider.bindings[binding.InstanceID][request.SourceID]) == 0 {
		return pluginsdk.DatasetReference{}, &pluginsdk.RuntimeError{Code: pluginsdk.ErrorPermissionDenied, Message: "dataset source/version is not bound to instance"}
	}
	// Reuse one immutable handle per instance/source to keep the registry bounded.
	for _, reference := range provider.handles {
		if reference.InstanceID == binding.InstanceID && reference.SourceID == request.SourceID {
			return reference, nil
		}
	}
	if len(provider.handles) >= 4096 {
		return pluginsdk.DatasetReference{}, &pluginsdk.RuntimeError{Code: pluginsdk.ErrorResourceExhausted, Message: "dataset handle budget exceeded"}
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return pluginsdk.DatasetReference{}, err
	}
	reference := pluginsdk.DatasetReference{Handle: hex.EncodeToString(random[:]), InstanceID: binding.InstanceID, Generation: binding.Generation, SourceID: request.SourceID, VersionDigest: request.VersionDigest}
	provider.handles[reference.Handle] = reference
	return reference, nil
}

func (provider *DatasetGeneration) Query(ctx context.Context, authorization pluginsdk.PolicyHostCallAuthorization, request pluginsdk.DatasetQueryRequest) (pluginsdk.DatasetQueryResponse, error) {
	response := pluginsdk.DatasetQueryResponse{Reference: request.Reference, Status: pluginsdk.DatasetQueryUnauthorized}
	if request.Reference.Validate() != nil {
		return response, errors.New("dataset reference is invalid")
	}
	if !datasetAuthorizationAllowed(authorization) || request.Reference.ValidateFor(authorization.InstanceID, authorization.Generation) != nil {
		return response, nil
	}
	if request.Budget.Validate() != nil {
		return pluginsdk.DatasetQueryResponse{Reference: request.Reference, Status: pluginsdk.DatasetQueryBudgetExceeded}, nil
	}
	if err := request.Validate(); err != nil {
		return response, err
	}
	if provider == nil {
		return pluginsdk.DatasetQueryResponse{Reference: request.Reference, Status: pluginsdk.DatasetQueryUnavailable}, nil
	}
	queryContext, cancel := context.WithTimeout(ctx, time.Duration(request.Budget.MaxDurationMicros)*time.Microsecond)
	defer cancel()
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if provider.closed || provider.instanceGenerations[authorization.InstanceID] != authorization.Generation || provider.handles[request.Reference.Handle] != request.Reference {
		return pluginsdk.DatasetQueryResponse{Reference: request.Reference, Status: pluginsdk.DatasetQueryStaleReference}, nil
	}
	for _, selector := range request.Classifications {
		if !provider.bindings[authorization.InstanceID][request.Reference.SourceID][datasetSelectorKey(selector)] {
			return response, nil
		}
	}
	index := provider.indices[request.Reference.SourceID]
	if index == nil {
		return pluginsdk.DatasetQueryResponse{Reference: request.Reference, Status: pluginsdk.DatasetQueryUnavailable}, nil
	}
	return index.Query(queryContext, request)
}

func (provider *DatasetGeneration) Close() {
	if provider == nil {
		return
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.closed {
		return
	}
	provider.closed = true
	provider.indices = nil
	provider.handles = nil
	if provider.pool != nil {
		provider.pool.mu.Lock()
		defer provider.pool.mu.Unlock()
		for _, key := range provider.poolKeys {
			cached := provider.pool.values[key]
			if cached == nil {
				continue
			}
			cached.users--
			if cached.users == 0 {
				provider.pool.memory -= cached.memory
				delete(provider.pool.values, key)
			}
		}
	}
	provider.poolKeys = nil
}

// A future PolicyDatasetHost adapter must resolve the authenticated source
// handle in PolicyDatasetQueryRequest, construct DatasetQueryRequest.Address
// from that Host-owned source, and call Query with the enclosing admission
// context. This provider deliberately does not interpret plugin-supplied source
// addresses as trusted admission metadata.
