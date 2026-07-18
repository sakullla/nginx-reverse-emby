package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

const (
	hotRestartLineagePollInterval = 250 * time.Millisecond
	hotRestartShutdownTimeout     = 10 * time.Second
)

// superviseHotRestartLineage keeps the service manager's original main PID
// alive while following authority across replaceable hot-restart workers.
func (a *App) superviseHotRestartLineage(
	ctx context.Context,
	direct hotRestartProcess,
	journalPath string,
	firstIdentity hotrestart.Identity,
) error {
	if direct == nil {
		return errors.New("hot restart child process is required")
	}
	directDone := make(chan error, 1)
	go func() { directDone <- direct.Wait() }()
	select {
	case <-ctx.Done():
		return errors.Join(ctx.Err(), stopDirectHotRestartProcess(direct, directDone))
	case <-directDone:
	}

	journal := hotrestart.NewFileAuthorityJournal(journalPath)
	ticker := time.NewTicker(hotRestartLineagePollInterval)
	defer ticker.Stop()
	for {
		record, err := journal.Load()
		if err != nil {
			return fmt.Errorf("load hot restart authority journal: %w", err)
		}
		// The first child exited without installing a successor. Recovering this
		// stale record would incorrectly make the resource-less root authoritative.
		if record.Identity == firstIdentity && record.ParentPID == os.Getpid() {
			return errors.New("hot restart authority child exited without a successor")
		}
		owner, recovered, err := journal.Recover(record.Identity, platform.ProcessAlive)
		if err != nil {
			return fmt.Errorf("recover hot restart authority: %w", err)
		}
		pid := authorityOwnerPID(owner, recovered)
		if pid <= 0 || pid == os.Getpid() {
			return errors.New("hot restart authority lineage has no live worker")
		}

		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), stopDescendantHotRestartProcess(pid))
		case <-ticker.C:
		}
	}
}

func authorityOwnerPID(owner string, record hotrestart.AuthorityRecord) int {
	switch owner {
	case hotrestart.AuthorityOwnerParent:
		return record.ParentPID
	case hotrestart.AuthorityOwnerChild:
		return record.ChildPID
	default:
		return 0
	}
}

func stopDirectHotRestartProcess(process hotRestartProcess, done <-chan error) error {
	signalErr := process.Signal(os.Interrupt)
	timer := time.NewTimer(hotRestartShutdownTimeout)
	defer timer.Stop()
	select {
	case waitErr := <-done:
		return errors.Join(signalErr, waitErr)
	case <-timer.C:
		return errors.Join(signalErr, process.Abort())
	}
}

func stopDescendantHotRestartProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	signalErr := process.Signal(os.Interrupt)
	ticker := time.NewTicker(hotRestartLineagePollInterval)
	defer ticker.Stop()
	timer := time.NewTimer(hotRestartShutdownTimeout)
	defer timer.Stop()
	for platform.ProcessAlive(pid) {
		select {
		case <-ticker.C:
		case <-timer.C:
			return errors.Join(signalErr, process.Kill())
		}
	}
	return signalErr
}
