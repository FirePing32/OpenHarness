package runtime

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"openharness/spec"
)

// ---------------------------------------------------------------- memory

type memoryEntry struct {
	ID      string    `json:"id"`
	Scope   string    `json:"scope"`
	Text    string    `json:"text"`
	Created time.Time `json:"created"`
}

// MemoryStore is a small JSON-file store for agent-written long-term memory.
type MemoryStore struct {
	mu      sync.Mutex
	path    string
	scope   string
	entries []memoryEntry
}

func OpenMemory(cfg spec.MemorySpec) (*MemoryStore, error) {
	m := &MemoryStore{path: cfg.Path, scope: cfg.Scope}
	b, err := os.ReadFile(cfg.Path)
	if err == nil {
		if err := json.Unmarshal(b, &m.entries); err != nil {
			return nil, fmt.Errorf("memory file %s is corrupt: %w", cfg.Path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return m, nil
}

func (m *MemoryStore) key(user string) string {
	if m.scope == "global" {
		return "global"
	}
	return user
}

func (m *MemoryStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m.entries, "", "  ")
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *MemoryStore) Add(user, text string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("m%d", time.Now().UnixNano())
	m.entries = append(m.entries, memoryEntry{ID: id, Scope: m.key(user), Text: text, Created: time.Now().UTC()})
	return id, m.saveLocked()
}

func (m *MemoryStore) Delete(user, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(user)
	for i, e := range m.entries {
		if e.ID == id && e.Scope == k {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			return true, m.saveLocked()
		}
	}
	return false, nil
}

// Search ranks entries by query-term overlap; an empty query returns the most recent.
func (m *MemoryStore) Search(user, query string, limit int) []memoryEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(user)
	terms := strings.Fields(strings.ToLower(query))
	type scored struct {
		e     memoryEntry
		score int
	}
	var hits []scored
	for _, e := range m.entries {
		if e.Scope != k {
			continue
		}
		s := 0
		lt := strings.ToLower(e.Text)
		for _, t := range terms {
			if strings.Contains(lt, t) {
				s++
			}
		}
		if len(terms) == 0 || s > 0 {
			hits = append(hits, scored{e, s})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].e.Created.After(hits[j].e.Created)
	})
	var out []memoryEntry
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].e)
	}
	return out
}

// ---------------------------------------------------------------- sessions

// SessionStore persists conversation history as one append-only JSONL file per
// session ("model-visible means logged").
type SessionStore struct {
	mu    sync.Mutex
	dir   string
	limit int
}

func NewSessionStore(cfg spec.SessionSpec) *SessionStore {
	return &SessionStore{dir: cfg.Dir, limit: cfg.MaxMessages}
}

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func (s *SessionStore) file(id string) string {
	if !safeID.MatchString(id) {
		h := sha256.Sum256([]byte(id))
		id = hex.EncodeToString(h[:12])
	}
	return filepath.Join(s.dir, id+".jsonl")
}

func (s *SessionStore) Load(id string) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.file(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var msgs []Message
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var m Message
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			msgs = append(msgs, m)
		}
	}
	if s.limit > 0 && len(msgs) > s.limit {
		msgs = trimToBoundary(msgs[len(msgs)-s.limit:])
	}
	return msgs, sc.Err()
}

func (s *SessionStore) Append(id string, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.file(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		w.Write(b)
		w.WriteByte('\n')
	}
	return w.Flush()
}

// trimToBoundary drops leading tool results whose tool call was cut off.
func trimToBoundary(msgs []Message) []Message {
	for len(msgs) > 0 && msgs[0].Role == "tool" {
		msgs = msgs[1:]
	}
	return msgs
}
