package semantic

import (
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type record struct {
	Talker  string
	Seq     int64
	Sender  string
	IsSelf  bool
	Type    int64
	SubType int64
	TS      int64
	Content string
	Model   string
	Dim     int
	Vector  []float64
}

type Store struct {
	db   *sql.DB
	path string
	mu   sync.Mutex
}

func OpenStore(workDir string) (*Store, error) {
	baseDir := stringsOr(filepath.Join(os.TempDir(), "chatlog_semantic"), workDir)
	if workDir != "" {
		baseDir = filepath.Join(workDir, ".chatlog_semantic")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(baseDir, "vector_index.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, path: dbPath}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Path() string { return s.path }

func (s *Store) init() error {
	schema := `
CREATE TABLE IF NOT EXISTS semantic_embeddings (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	talker TEXT NOT NULL,
	seq INTEGER NOT NULL,
	sender TEXT,
	is_self INTEGER NOT NULL DEFAULT 0,
	msg_type INTEGER NOT NULL DEFAULT 0,
	msg_sub_type INTEGER NOT NULL DEFAULT 0,
	ts INTEGER NOT NULL,
	content TEXT NOT NULL,
	content_hash TEXT NOT NULL,
	model TEXT NOT NULL,
	dim INTEGER NOT NULL,
	vector_json TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_semantic_unique ON semantic_embeddings(talker, seq, model, dim);
CREATE INDEX IF NOT EXISTS idx_semantic_talker_ts ON semantic_embeddings(talker, ts);
CREATE INDEX IF NOT EXISTS idx_semantic_ts ON semantic_embeddings(ts);

CREATE TABLE IF NOT EXISTS semantic_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM semantic_embeddings`)
	return err
}

func (s *Store) Count() (int, error) {
	row := s.db.QueryRow(`SELECT COUNT(1) FROM semantic_embeddings`)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) MaxSeq(talker, model string, dim int) (int64, error) {
	row := s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM semantic_embeddings WHERE talker=? AND model=? AND dim=?`, talker, model, dim)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) Upsert(records []record) error {
	if len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
INSERT INTO semantic_embeddings(
	talker, seq, sender, is_self, msg_type, msg_sub_type, ts,
	content, content_hash, model, dim, vector_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(talker, seq, model, dim) DO UPDATE SET
	sender=excluded.sender,
	is_self=excluded.is_self,
	msg_type=excluded.msg_type,
	msg_sub_type=excluded.msg_sub_type,
	ts=excluded.ts,
	content=excluded.content,
	content_hash=excluded.content_hash,
	vector_json=excluded.vector_json,
	updated_at=excluded.updated_at
`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, item := range records {
		vecRaw, err := json.Marshal(item.Vector)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := stmt.Exec(
			item.Talker, item.Seq, item.Sender, boolToInt(item.IsSelf), item.Type, item.SubType, item.TS,
			item.Content, hashText(item.Content), item.Model, item.Dim, string(vecRaw), now,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadCandidates(talker, model string, dim, limit int) ([]record, error) {
	if limit <= 0 {
		limit = 5000
	}
	query := `SELECT talker, seq, sender, is_self, msg_type, msg_sub_type, ts, content, model, dim, vector_json
FROM semantic_embeddings
WHERE model=? AND dim=?`
	args := []any{model, dim}
	if talker != "" {
		query += ` AND talker=?`
		args = append(args, talker)
	}
	query += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]record, 0, limit)
	for rows.Next() {
		var item record
		var isSelf int
		var vecRaw string
		if err := rows.Scan(
			&item.Talker, &item.Seq, &item.Sender, &isSelf, &item.Type, &item.SubType,
			&item.TS, &item.Content, &item.Model, &item.Dim, &vecRaw,
		); err != nil {
			return nil, err
		}
		item.IsSelf = isSelf == 1
		if err := json.Unmarshal([]byte(vecRaw), &item.Vector); err != nil {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Store) SaveMeta(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO semantic_meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) DeleteMeta(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM semantic_meta WHERE key=?`, key)
	return err
}

func (s *Store) GetMeta(key string) (string, error) {
	row := s.db.QueryRow(`SELECT value FROM semantic_meta WHERE key=?`, key)
	var value string
	if err := row.Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func stringsOr(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func hashText(s string) string {
	h := sha1.Sum([]byte(s))
	return fmt.Sprintf("%x", h[:])
}
