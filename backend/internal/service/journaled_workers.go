package service

import (
	"context"
	"docktun/internal/db"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (s *SelfUpdater) StartJournaled(ctx context.Context, store *OperationStore) (SelfUpdateState, error) {
	if store == nil {
		return s.Start(ctx)
	}
	job, err := store.Begin("core_update", "maintenance:core", map[string]any{})
	if err != nil {
		return s.Status(), err
	}
	worker := &SelfUpdater{statePath: s.statePath, operationID: job.ID, operationStore: store}
	result, err := worker.Start(ctx)
	if err != nil {
		active, uncertain := journalWorkerState(result.Helper)
		if active {
			result.Phase = "pulling"
			result.Message = "Independent worker is active; reconnect to inspect progress"
			return result, nil
		}
		if uncertain {
			_ = store.Phase(job.ID, "interrupted", "Worker launch outcome is unknown; inspect the worker before acknowledging recovery")
		} else {
			_ = store.Finish(job.ID, err)
		}
	}
	return result, err
}
func (s *ManagedServices) StartJournaled(ctx context.Context, action string, ids []string, store *OperationStore) (ServiceUpdateJob, error) {
	if store == nil {
		return s.Start(ctx, action, ids)
	}
	job, err := store.Begin("service_"+action, "maintenance:services", map[string]any{})
	if err != nil {
		return ServiceUpdateJob{}, err
	}
	worker := &ManagedServices{statePath: s.statePath, operationID: job.ID, operationStore: store}
	result, err := worker.Start(ctx, action, ids)
	if err != nil {
		active, uncertain := journalWorkerState(result.Helper)
		if active {
			result.Phase = "checking"
			result.Message = "Independent worker is active; reconnect to inspect progress"
			return result, nil
		}
		if uncertain {
			_ = store.Phase(job.ID, "interrupted", "Worker launch outcome is unknown; inspect the worker before acknowledging recovery")
		} else {
			_ = store.Finish(job.ID, err)
		}
	}
	return result, err
}
func RunJournaledWorker(target, jobID string, run func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	core, err := inspectUpdateContainer(ctx, target)
	if err != nil {
		return err
	}
	database := coreEnvironment(core, "DB_PATH", "/app/data/app.db")
	if !filepath.IsAbs(database) {
		workdir, _ := core.Config["WorkingDir"].(string)
		if workdir == "" {
			workdir = "/"
		}
		database = filepath.Join(workdir, database)
	}
	if _, err = os.Stat(database); err != nil {
		return fmt.Errorf("worker database unavailable: %w", err)
	}
	connection, err := db.New(database)
	if err != nil {
		return err
	}
	defer connection.Close()
	store := NewOperationStore(connection)
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	if err = store.SetWorker(jobID, host); err != nil {
		return err
	}
	err = run()
	finishErr := store.Finish(jobID, err)
	if err != nil {
		return err
	}
	return finishErr
}

func journalWorkerState(helper string) (active, uncertain bool) {
	if helper == "" {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if c, err := inspectUpdateContainer(ctx, helper); err == nil {
		return c.State.Running, false
	}
	if _, err := updateDocker(ctx, "info", "--format", "{{.ID}}"); err != nil {
		return false, true
	}
	return false, false
}
