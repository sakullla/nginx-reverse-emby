package acmeflow

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
)

type ReconcileResult struct {
	Current                    *Generation
	PendingChallenges          []ChallengeIntent
	RemovedStages              []string
	RemovedCompletedChallenges []string
}

// Reconcile removes only uncommitted stage directories and completed
// challenge intents. Committed generations and both current fallback slots are
// retained, so repeated reconciliation cannot delete the serving generation.
func (store *StateStore) Reconcile(ctx context.Context) (ReconcileResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	var result ReconcileResult
	if err := contextError(ctx); err != nil {
		return result, normalizeError("state_reconcile", err)
	}
	current, err := store.loadCurrentLocked(ctx)
	switch {
	case err == nil:
		current = cloneGeneration(current)
		result.Current = &current
	case errors.Is(err, ErrNoCurrentGeneration):
	default:
		return result, err
	}

	stageEntries, err := store.fs.readDirectory(stagingDirectory)
	if err != nil {
		return result, WrapError(CategoryProtocol, "state_reconcile", err)
	}
	for _, entry := range stageEntries {
		if err := contextError(ctx); err != nil {
			return result, normalizeError("state_reconcile", err)
		}
		name := entry.Name()
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			return result, WrapError(CategoryProtocol, "state_reconcile", errors.New("staging directory contains an invalid entry"))
		}
		if err := store.fs.removeAll(statePath(stagingDirectory, name), "reconcile.stage"); err != nil {
			return result, WrapError(CategoryProtocol, "state_reconcile", err)
		}
		result.RemovedStages = append(result.RemovedStages, name)
	}
	sort.Strings(result.RemovedStages)

	intents, err := store.listChallengeIntentsLocked(ctx)
	if err != nil {
		return result, err
	}
	for _, intent := range intents {
		switch intent.Status {
		case ChallengeIntentPending:
			result.PendingChallenges = append(result.PendingChallenges, intent)
		case ChallengeIntentCompleted:
			if err := store.fs.removeFile(
				statePath(challengesDirectory, intent.ID+".json"),
				"reconcile.challenge",
			); err != nil && !errors.Is(err, os.ErrNotExist) {
				return result, WrapError(CategoryChallenge, "state_reconcile", err)
			}
			result.RemovedCompletedChallenges = append(result.RemovedCompletedChallenges, intent.ID)
		}
	}
	sort.Slice(result.PendingChallenges, func(i, j int) bool {
		return result.PendingChallenges[i].ID < result.PendingChallenges[j].ID
	})
	sort.Strings(result.RemovedCompletedChallenges)
	return result, nil
}
