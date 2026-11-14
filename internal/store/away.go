package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrAwayInactive = errors.New("away session inactive")

type AwaySession struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	EndsAt      time.Time  `json:"ends_at"`
	StoppedAt   *time.Time `json:"stopped_at,omitempty"`
	Sender      string     `json:"sender"`
	AccountKey  string     `json:"-"`
	ModelKey    string     `json:"-"`
	UIDValidity uint32     `json:"-"`
	NextUID     uint32     `json:"-"`
}

type AwayTask struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Requirements   []string  `json:"requirements"`
	Deadline       string    `json:"deadline,omitempty"`
	Evidence       string    `json:"evidence,omitempty"`
	Draft          string    `json:"draft,omitempty"`
	DraftFailed    bool      `json:"draft_failed,omitempty"`
	Done           bool      `json:"done,omitempty"`
	Subject        string    `json:"subject"`
	Sender         string    `json:"sender"`
	ReceivedAt     time.Time `json:"received_at"`
	Body           string    `json:"body"`
	ConversationID string    `json:"-"`
}

type AwayEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

type AwaySnapshot struct {
	Session *AwaySession `json:"session,omitempty"`
	Tasks   []AwayTask   `json:"tasks"`
	Events  []AwayEvent  `json:"events"`
}

const awaySessionFields = "id,status,started_ms,ends_ms,stopped_ms,sender,account_key,model_key,uid_validity,next_uid"
const awayTaskFields = "t.id,t.title,t.requirements,t.deadline,t.evidence,t.draft,t.state,t.done,m.email_json,t.received_ms,m.body,m.conversation_id"
const awayTaskFrom = " FROM away_tasks t JOIN messages m ON m.id=t.id "

func scanAwaySession(row interface{ Scan(...any) error }) (*AwaySession, error) {
	var item AwaySession
	var start, end, stop int64
	err := row.Scan(&item.ID, &item.Status, &start, &end, &stop, &item.Sender, &item.AccountKey, &item.ModelKey, &item.UIDValidity, &item.NextUID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	item.StartedAt = time.UnixMilli(start).UTC()
	item.EndsAt = time.UnixMilli(end).UTC()
	if stop != 0 {
		at := time.UnixMilli(stop).UTC()
		item.StoppedAt = &at
	}
	return &item, nil
}

func scanAwayTask(row interface{ Scan(...any) error }) (AwayTask, error) {
	var item AwayTask
	var requirements, state, headers string
	var received int64
	if err := row.Scan(&item.ID, &item.Title, &requirements, &item.Deadline, &item.Evidence, &item.Draft, &state, &item.Done, &headers, &received, &item.Body, &item.ConversationID); err != nil {
		return AwayTask{}, err
	}
	var source EmailHeaders
	if json.Unmarshal([]byte(headers), &source) != nil || json.Unmarshal([]byte(requirements), &item.Requirements) != nil {
		return AwayTask{}, ErrUnavailable
	}
	item.Subject, item.Sender = source.Subject, source.FromAddress
	item.DraftFailed = state == "failed"
	item.ReceivedAt = time.UnixMilli(received).UTC()
	return item, nil
}

func awayEvent(tx *sql.Tx, id string, at int64, kind, detail string) error {
	_, err := tx.Exec(`INSERT INTO away_events(session_id,at_ms,kind,detail) VALUES (?,?,?,?)`, id, at, kind, detail)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) awayWrite(fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) AwaySnapshot() (AwaySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := AwaySnapshot{Tasks: []AwayTask{}, Events: []AwayEvent{}}
	session, err := scanAwaySession(s.db.QueryRow("SELECT " + awaySessionFields + " FROM away_sessions ORDER BY started_ms DESC,rowid DESC LIMIT 1"))
	if err != nil || session == nil {
		return result, err
	}
	result.Session = session
	rows, err := s.db.Query("SELECT "+awayTaskFields+awayTaskFrom+" WHERE t.session_id=? ORDER BY t.received_ms,t.id", session.ID)
	if err != nil {
		return result, ErrUnavailable
	}
	for rows.Next() {
		item, err := scanAwayTask(rows)
		if err != nil {
			_ = rows.Close()
			return result, ErrUnavailable
		}
		result.Tasks = append(result.Tasks, item)
	}
	_ = rows.Close()
	if rows.Err() != nil {
		return result, ErrUnavailable
	}
	rows, err = s.db.Query(`SELECT at_ms,kind,detail FROM away_events WHERE session_id=? ORDER BY seq`, session.ID)
	if err != nil {
		return result, ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var event AwayEvent
		var at int64
		if rows.Scan(&at, &event.Kind, &event.Detail) != nil {
			return result, ErrUnavailable
		}
		event.At = time.UnixMilli(at).UTC()
		result.Events = append(result.Events, event)
	}
	if rows.Err() != nil {
		return result, ErrUnavailable
	}
	return result, nil
}

func (s *Store) RunningAway() (*AwaySession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return scanAwaySession(s.db.QueryRow("SELECT " + awaySessionFields + " FROM away_sessions WHERE status='running'"))
}

func (s *Store) StartAway(item AwaySession) error {
	return s.awayWrite(func(tx *sql.Tx) error {
		active, err := scanAwaySession(tx.QueryRow("SELECT " + awaySessionFields + " FROM away_sessions WHERE status='running'"))
		if err != nil {
			return err
		}
		if active != nil {
			if time.Now().Before(active.EndsAt) {
				return ErrConflict
			}
			if err := finishAway(tx, active.ID, "ended", ""); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`INSERT INTO away_sessions(id,status,started_ms,ends_ms,sender,account_key,model_key,uid_validity,next_uid) VALUES(?,'running',?,?,?,?,?,?,?)`, item.ID, item.StartedAt.UnixMilli(), item.EndsAt.UnixMilli(), item.Sender, item.AccountKey, item.ModelKey, item.UIDValidity, item.NextUID)
		if err != nil {
			return ErrUnavailable
		}
		return awayEvent(tx, item.ID, item.StartedAt.UnixMilli(), "started", "")
	})
}

func finishAway(tx *sql.Tx, id, status, detail string) error {
	now := time.Now().UnixMilli()
	result, err := tx.Exec(`UPDATE away_sessions SET status=?, stopped_ms=CASE WHEN ?='stopped' THEN ? ELSE 0 END WHERE id=? AND status='running'`, status, status, now, id)
	if err != nil {
		return ErrUnavailable
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrAwayInactive
	}
	if _, err := tx.Exec(`UPDATE away_tasks SET state='failed' WHERE session_id=? AND state IN ('queued','processing')`, id); err != nil {
		return ErrUnavailable
	}
	return awayEvent(tx, id, now, status, detail)
}

func (s *Store) FinishAway(id, status, detail string) error {
	return s.awayWrite(func(tx *sql.Tx) error { return finishAway(tx, id, status, detail) })
}

func activeAway(tx *sql.Tx, id string) (int64, error) {
	now := time.Now().UnixMilli()
	var found string
	err := tx.QueryRow(`SELECT id FROM away_sessions WHERE id=? AND status='running' AND ends_ms>?`, id, now).Scan(&found)
	if err == sql.ErrNoRows {
		return now, ErrAwayInactive
	}
	if err != nil {
		return now, ErrUnavailable
	}
	return now, nil
}

// QueueAwayMail commits the inbox evidence, task, and audit entries together.
func (s *Store) QueueAwayMail(sessionID string, req ImportRequest, received time.Time) error {
	req, err := normalizeImport(req)
	if err != nil || req.Platform != "email" || len(req.Messages) != 1 || req.Messages[0].Email == nil {
		return ErrInvalidInput
	}
	m := req.Messages[0]
	connection := stableID("connection", req.Platform, req.AccountID)
	conversation := stableID("conversation", connection, m.PeerID)
	id := stableID("message", conversation, m.ExternalID)
	return s.awayWrite(func(tx *sql.Tx) error {
		now, err := activeAway(tx, sessionID)
		if err != nil {
			return err
		}
		if _, err := importIntoTx(context.Background(), tx, req, "imap"); err != nil {
			return err
		}
		result, err := tx.Exec(`INSERT INTO away_tasks(id,session_id,received_ms,title,state) VALUES(?,?,?,?,'queued') ON CONFLICT(id) DO NOTHING`, id, sessionID, received.UnixMilli(), m.PeerName)
		if err != nil {
			return ErrUnavailable
		}
		inserted, _ := result.RowsAffected()
		if inserted == 0 {
			return nil
		}
		if err := awayEvent(tx, sessionID, now, "received", m.Email.Subject); err != nil {
			return err
		}
		return awayEvent(tx, sessionID, now, "task", m.PeerName)
	})
}

func (s *Store) AwaySyncError(id, detail string) error {
	return s.awayWrite(func(tx *sql.Tx) error {
		now, err := activeAway(tx, id)
		if err != nil {
			return err
		}
		var previousKind, previousDetail string
		_ = tx.QueryRow(`SELECT kind,detail FROM away_events WHERE session_id=? ORDER BY seq DESC LIMIT 1`, id).Scan(&previousKind, &previousDetail)
		if previousKind == "sync_error" && previousDetail == detail {
			return nil
		}
		return awayEvent(tx, id, now, "sync_error", detail)
	})
}

func (s *Store) RecoverAwayTask(id string) error {
	return s.awayWrite(func(tx *sql.Tx) error {
		now, err := activeAway(tx, id)
		if err != nil {
			return err
		}
		result, err := tx.Exec(`UPDATE away_tasks SET state='failed' WHERE session_id=? AND state='processing'`, id)
		if err != nil {
			return ErrUnavailable
		}
		if changed, _ := result.RowsAffected(); changed > 0 {
			return awayEvent(tx, id, now, "draft_error", "服务曾重启。已保留任务，可手动补写回信。")
		}
		return nil
	})
}

func (s *Store) TakeAwayTask(id string) (*AwayTask, error) {
	var item AwayTask
	err := s.awayWrite(func(tx *sql.Tx) error {
		if _, err := activeAway(tx, id); err != nil {
			return err
		}
		var err error
		item, err = scanAwayTask(tx.QueryRow("SELECT "+awayTaskFields+awayTaskFrom+" WHERE t.session_id=? AND t.state='queued' ORDER BY t.received_ms LIMIT 1", id))
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return ErrUnavailable
		}
		_, err = tx.Exec(`UPDATE away_tasks SET state='processing' WHERE id=?`, item.ID)
		if err != nil {
			return ErrUnavailable
		}
		return nil
	})
	if item.ID == "" || err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) CompleteAwayTask(id string, task AwayTask, failed string) error {
	return s.awayWrite(func(tx *sql.Tx) error {
		now, err := activeAway(tx, id)
		if err != nil {
			return err
		}
		state, kind, detail := "ready", "draft", "英文回信已保存"
		if failed != "" {
			state, kind, detail = "failed", "draft_error", failed
		}
		requirements, _ := json.Marshal(task.Requirements)
		result, err := tx.Exec(`UPDATE away_tasks SET title=?, requirements=?, deadline=?, evidence=?, draft=?, state=? WHERE session_id=? AND id=? AND state='processing'`, task.Title, string(requirements), task.Deadline, task.Evidence, task.Draft, state, id, task.ID)
		if err != nil {
			return ErrUnavailable
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return nil
		}
		return awayEvent(tx, id, now, kind, detail)
	})
}

func (s *Store) UpdateAwayTask(sessionID, id string, draft *string, done *bool) error {
	return s.awayWrite(func(tx *sql.Tx) error {
		result, err := tx.Exec(`UPDATE away_tasks SET done=COALESCE(?,done), draft=COALESCE(?,draft), state=CASE WHEN ? IS NULL THEN state ELSE 'ready' END WHERE session_id=? AND id=?`, done, draft, draft, sessionID, id)
		if err != nil {
			return ErrUnavailable
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *Store) AdvanceAway(id string, uid uint32) error {
	return s.awayWrite(func(tx *sql.Tx) error {
		if _, err := activeAway(tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE away_sessions SET next_uid=max(next_uid,?) WHERE id=?`, uid, id); err != nil {
			return ErrUnavailable
		}
		return nil
	})
}
