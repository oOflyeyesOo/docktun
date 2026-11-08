package service

import (
	"database/sql"
	"docktun/internal/db"
	"fmt"
	"github.com/google/uuid"
	"time"
)

type QueuedIncident struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Message string `json:"message"`
}
type AlertDelivery struct {
	ID          string        `json:"id"`
	Key         string        `json:"key"`
	Kind        string        `json:"kind"`
	Message     string        `json:"message"`
	Phase       string        `json:"phase"`
	Attempts    int           `json:"attempts"`
	NextAttempt int64         `json:"next_attempt"`
	CreatedAt   int64         `json:"created_at"`
	DeliveredAt sql.NullInt64 `json:"delivered_at"`
	LastError   string        `json:"last_error"`
}
type AlertQueue struct{ DB *db.DB }

func (q AlertQueue) State() (string, error) {
	var raw string
	err := q.DB.QueryRow(`SELECT value FROM monitor_state WHERE key='notifications'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return raw, err
}
func (q AlertQueue) Apply(events []QueuedIncident, state string, now int64) error {
	tx, err := q.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, event := range events {
		if event.Kind == "recovery" {
			for _, kind := range []string{"container_stop", "tunnel_down", "high_usage"} {
				if _, err = tx.Exec(`UPDATE alert_outbox SET phase='superseded',last_error='Condition recovered before delivery' WHERE event_key=? AND phase IN ('pending','retry','failed')`, kind+":"+event.Key); err != nil {
					return err
				}
			}
		} else {
			if _, err = tx.Exec(`UPDATE alert_outbox SET phase='superseded' WHERE event_key=? AND phase IN ('pending','retry','failed')`, "recovery:"+event.Key); err != nil {
				return err
			}
		}
		var count int
		if err = tx.QueryRow(`SELECT count(*) FROM alert_outbox WHERE phase IN ('pending','retry')`).Scan(&count); err != nil {
			return err
		}
		if count >= 128 {
			return fmt.Errorf("notification queue is full; delivery history needs attention")
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO alert_outbox(id,event_key,kind,message,phase,attempts,next_attempt,created_at) VALUES(?,?,?,?,'pending',0,?,?)`, uuid.NewString(), event.Kind+":"+event.Key, event.Kind, event.Message, now, now); err != nil {
			return err
		}
	}
	if state != "" {
		if _, err = tx.Exec(`INSERT INTO monitor_state(key,value) VALUES('notifications',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, state); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (q AlertQueue) List(pending bool, now int64, target ...string) ([]AlertDelivery, error) {
	query := `SELECT id,event_key,kind,message,phase,attempts,next_attempt,created_at,delivered_at,last_error FROM alert_outbox`
	args := []any{}
	if pending {
		query += ` WHERE phase IN ('pending','retry') AND next_attempt<=?`
		args = append(args, now)
	}
	if len(target) > 0 && target[0] != "" {
		if pending {
			query += " AND "
		} else {
			query += " WHERE "
		}
		query += `event_key IN (?,?,?)`
		args = append(args, "container_stop:"+target[0], "tunnel_down:"+target[0], "recovery:"+target[0])
	}
	query += ` ORDER BY created_at DESC,id LIMIT 100`
	rows, err := q.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AlertDelivery{}
	for rows.Next() {
		var d AlertDelivery
		if err = rows.Scan(&d.ID, &d.Key, &d.Kind, &d.Message, &d.Phase, &d.Attempts, &d.NextAttempt, &d.CreatedAt, &d.DeliveredAt, &d.LastError); err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	return items, rows.Err()
}
func (q AlertQueue) Result(id string, cause error, now int64) error {
	if cause == nil {
		_, err := q.DB.Exec(`UPDATE alert_outbox SET phase='delivered',delivered_at=?,last_error='' WHERE id=?`, now, id)
		return err
	}
	var attempts int
	if err := q.DB.QueryRow(`SELECT attempts FROM alert_outbox WHERE id=?`, id).Scan(&attempts); err != nil {
		return err
	}
	attempts++
	phase := "retry"
	if attempts >= 8 {
		phase = "failed"
	}
	delay := 30 * time.Second * time.Duration(1<<min(attempts-1, 7))
	if delay > time.Hour {
		delay = time.Hour
	}
	_, err := q.DB.Exec(`UPDATE alert_outbox SET phase=?,attempts=?,next_attempt=?,last_error='Delivery failed; inspect destination access and manual test' WHERE id=?`, phase, attempts, now+int64(delay.Seconds()), id)
	return err
}
func (q AlertQueue) Retry(id string) error {
	result, err := q.DB.Exec(`UPDATE alert_outbox SET phase='retry',attempts=0,next_attempt=?,last_error='' WHERE id=? AND phase='failed'`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("only exhausted deliveries can be retried; recovered incidents remain superseded")
	}
	return err
}
