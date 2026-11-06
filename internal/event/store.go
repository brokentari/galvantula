package event

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS events (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,
	run     TEXT    NOT NULL,
	at      TEXT    NOT NULL,
	type    TEXT    NOT NULL,
	node    TEXT    NOT NULL DEFAULT '',
	attempt INTEGER NOT NULL DEFAULT 0,
	data    TEXT
);
CREATE INDEX IF NOT EXISTS events_by_run ON events (run, seq);
`

// subBuffer is how many events a slow subscriber may fall behind before the
// store drops it. A dropped SSE client reconnects with Last-Event-ID.
const subBuffer = 256

// Store is the SQLite event log. It is safe for concurrent use.
type Store struct {
	db   *sql.DB
	mu   sync.Mutex // orders appends and guards subs
	subs map[chan Event]string
}

// Open opens or creates the log at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema in %s: %w", path, err)
	}
	return &Store{db: db, subs: map[chan Event]string{}}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Append writes e, sets its Seq and At, and sends it to subscribers of its run.
func (s *Store) Append(e Event) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.At = time.Now().UTC()
	var data any
	if e.Data != nil {
		data = string(e.Data)
	}
	res, err := s.db.Exec(`INSERT INTO events (run, at, type, node, attempt, data) VALUES (?, ?, ?, ?, ?, ?)`,
		e.Run, e.At.Format(time.RFC3339Nano), e.Type, e.Node, e.Attempt, data)
	if err != nil {
		return e, fmt.Errorf("append %s event: %w", e.Type, err)
	}
	if e.Seq, err = res.LastInsertId(); err != nil {
		return e, err
	}
	for ch, run := range s.subs {
		if run != e.Run {
			continue
		}
		select {
		case ch <- e:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
	return e, nil
}

// Events returns a run's events with Seq greater than after, in order.
func (s *Store) Events(run string, after int64) ([]Event, error) {
	rows, err := s.db.Query(`SELECT seq, run, at, type, node, attempt, data FROM events WHERE run = ? AND seq > ? ORDER BY seq`, run, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var (
			e    Event
			at   string
			data sql.NullString
		)
		if err := rows.Scan(&e.Seq, &e.Run, &at, &e.Type, &e.Node, &e.Attempt, &data); err != nil {
			return nil, err
		}
		if e.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("event %d: %w", e.Seq, err)
		}
		if data.Valid {
			e.Data = json.RawMessage(data.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RunSummary is one row in the list of runs.
type RunSummary struct {
	ID      string    `json:"id"`
	Graph   string    `json:"graph"`
	Created time.Time `json:"created"`
	Status  RunStatus `json:"status"`
}

// Runs lists every run, newest first.
func (s *Store) Runs() ([]RunSummary, error) {
	rows, err := s.db.Query(`
		SELECT c.run, c.at, c.data, f.data
		FROM events c
		LEFT JOIN events f ON f.run = c.run AND f.type = ?
		WHERE c.type = ?
		ORDER BY c.seq DESC`, RunFinished, RunCreated)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunSummary
	for rows.Next() {
		var (
			r            RunSummary
			at, created  string
			finished     sql.NullString
			createdData  RunCreatedData
			finishedData RunFinishedData
		)
		if err := rows.Scan(&r.ID, &at, &created, &finished); err != nil {
			return nil, err
		}
		if r.Created, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(created), &createdData); err != nil {
			return nil, fmt.Errorf("run %s: %w", r.ID, err)
		}
		r.Graph, r.Status = createdData.Graph.Name, RunRunning
		if finished.Valid {
			if err := json.Unmarshal([]byte(finished.String), &finishedData); err != nil {
				return nil, fmt.Errorf("run %s: %w", r.ID, err)
			}
			r.Status = finishedData.Status
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Subscribe returns a channel that receives a run's new events, and a
// function that ends the subscription. The store closes the channel if
// the reader falls more than subBuffer events behind.
func (s *Store) Subscribe(run string) (<-chan Event, func()) {
	ch := make(chan Event, subBuffer)
	s.mu.Lock()
	s.subs[ch] = run
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
}
