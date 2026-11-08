package service

import (
	"context"
	"docktun/internal/db"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"strings"
	"time"
)

type Operation struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Target    string   `json:"target"`
	Phase     string   `json:"phase"`
	Message   string   `json:"message"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
	Resources []string `json:"resources,omitempty"`
}
type OperationStore struct {
	DB    *db.DB
	Owner string
}

var operationOwner = uuid.NewString()

func NewOperationStore(database *db.DB) *OperationStore {
	return &OperationStore{DB: database, Owner: operationOwner}
}
func (s *OperationStore) Begin(kind, target string, snapshot any) (Operation, error) {
	job := Operation{ID: uuid.NewString(), Kind: kind, Target: target, Phase: "preparing", Message: "Preparing operation", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return job, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return job, err
	}
	defer tx.Rollback()
	var busy int
	if strings.HasPrefix(target, "maintenance:") {
		err = tx.QueryRow(`SELECT count(*) FROM operation_locks`).Scan(&busy)
	} else {
		err = tx.QueryRow(`SELECT count(*) FROM operation_locks WHERE target LIKE 'maintenance:%'`).Scan(&busy)
	}
	if err != nil {
		return job, err
	}
	if busy > 0 {
		return job, fmt.Errorf("maintenance conflicts with an active operation")
	}
	if _, err = tx.Exec(`INSERT INTO operation_locks(target,operation_id) VALUES(?,?)`, target, job.ID); err != nil {
		return job, fmt.Errorf("an operation or interrupted recovery already owns this target; inspect its history first")
	}
	if _, err = tx.Exec(`INSERT INTO operations(id,kind,target,phase,message,created_at,updated_at,owner,snapshot) VALUES(?,?,?,?,?,?,?,?,?)`, job.ID, kind, target, job.Phase, job.Message, job.CreatedAt, job.UpdatedAt, s.Owner, string(raw)); err != nil {
		return job, err
	}
	return job, tx.Commit()
}
func (s *OperationStore) Phase(id, phase, message string) error {
	_, err := s.DB.Exec(`UPDATE operations SET phase=?,message=?,updated_at=? WHERE id=?`, phase, message, time.Now().Unix(), id)
	return err
}

type operationRecoveryRequired struct{ cause error }

func (e *operationRecoveryRequired) Error() string { return e.cause.Error() }
func (e *operationRecoveryRequired) Unwrap() error { return e.cause }
func (s *OperationStore) Finish(id string, cause error) error {
	var recovery *operationRecoveryRequired
	if errors.As(cause, &recovery) {
		return s.Phase(id, "interrupted", "Recovery needs manual inspection of the listed resources before acknowledgement; the operation lock was retained")
	}
	phase, message := "complete", "Operation completed"
	if cause != nil {
		phase, message = "failed", "Operation failed; inspect target state and action logs before retrying"
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE operations SET phase=?,message=?,updated_at=? WHERE id=?`, phase, message, time.Now().Unix(), id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM operation_locks WHERE operation_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *OperationStore) List(target string) ([]Operation, error) {
	query := `SELECT id,kind,target,phase,message,created_at,updated_at,snapshot FROM operations`
	args := []any{}
	if target != "" {
		query += ` WHERE target=?`
		args = append(args, target)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT 100`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Operation{}
	for rows.Next() {
		var op Operation
		var snapshot string
		if err = rows.Scan(&op.ID, &op.Kind, &op.Target, &op.Phase, &op.Message, &op.CreatedAt, &op.UpdatedAt, &snapshot); err != nil {
			return nil, err
		}
		var saved struct {
			Resources []string `json:"resources"`
		}
		_ = json.Unmarshal([]byte(snapshot), &saved)
		op.Resources = saved.Resources
		result = append(result, op)
	}
	return result, rows.Err()
}
func (s *OperationStore) SetWorker(id, worker string) error {
	return s.MergeSnapshot(id, map[string]any{"worker": worker})
}
func (s *OperationStore) MergeSnapshot(id string, values map[string]any) error {
	var raw string
	if err := s.DB.QueryRow(`SELECT snapshot FROM operations WHERE id=?`, id).Scan(&raw); err != nil {
		return err
	}
	snapshot := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return err
	}
	for key, value := range values {
		snapshot[key] = value
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE operations SET snapshot=? WHERE id=?`, string(data), id)
	return err
}
func (s *OperationStore) ReconcileInterrupted() error {
	rows, err := s.DB.Query(`SELECT id,snapshot,owner,created_at FROM operations WHERE phase IN ('preparing','running','checking','restoring')`)
	if err != nil {
		return err
	}
	type candidate struct {
		id, raw, owner string
		created        int64
	}
	items := []candidate{}
	for rows.Next() {
		var item candidate
		if err = rows.Scan(&item.id, &item.raw, &item.owner, &item.created); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		snapshot := map[string]any{}
		_ = json.Unmarshal([]byte(item.raw), &snapshot)
		worker, _ := snapshot["worker"].(string)
		if worker == "" && item.owner == s.Owner {
			continue
		}
		if worker != "" && time.Now().Unix()-item.created < 30 {
			continue
		}
		if worker != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			// A daemon outage is not evidence that the independent worker stopped.
			_, daemonErr := updateDocker(ctx, "info", "--format", "{{.ID}}")
			if daemonErr != nil {
				cancel()
				continue
			}
			helper, e := inspectUpdateContainer(ctx, worker)
			cancel()
			if e == nil && helper.State.Running {
				continue
			}
		}
		if _, err = s.DB.Exec(`UPDATE operations SET phase='interrupted',message='Operation interrupted. Inspect its target and recovery resources before acknowledging.',updated_at=? WHERE id=? AND phase IN ('preparing','running','checking','restoring')`, time.Now().Unix(), item.id); err != nil {
			return err
		}
	}
	return nil
}
func (s *OperationStore) Acknowledge(id string) error {
	var target, phase string
	if err := s.DB.QueryRow(`SELECT target,phase FROM operations WHERE id=?`, id).Scan(&target, &phase); err != nil {
		return err
	}
	if phase != "interrupted" {
		return fmt.Errorf("only interrupted operations need recovery acknowledgement")
	}
	if strings.HasPrefix(target, "container:") {
		ctx, cancel := operationContext()
		defer cancel()
		if _, err := inspectUpdateContainer(ctx, strings.TrimPrefix(target, "container:")); err != nil {
			return fmt.Errorf("target cannot be inspected; restore its original name/container before acknowledging")
		}
	}
	return s.Finish(id, fmt.Errorf("acknowledged interrupted operation"))
}
