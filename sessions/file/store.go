package file

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/core/actor/events"
	actorsink "github.com/basenana/friday/core/actor/sink"
	"github.com/basenana/friday/core/contextmgr"
	corelogger "github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
)

const (
	maxEventLogBytes    int64 = 8 << 20
	targetEventLogBytes int64 = 6 << 20
)

type FileSessionStore struct {
	basePath      string
	metaLocks     sync.Map // session ID -> *sync.Mutex
	eventLocks    sync.Map // session ID -> *sync.Mutex
	relationLocks sync.Map // root ID + relation key -> *sync.Mutex
}

func NewFileSessionStore(basePath string) *FileSessionStore {
	return &FileSessionStore{
		basePath: basePath,
	}
}

func (s *FileSessionStore) EnsureDir() error {
	return os.MkdirAll(s.basePath, 0755)
}

// Private methods - internal paths

func (s *FileSessionStore) sessionDir(id string) string {
	return filepath.Join(s.basePath, id)
}

func (s *FileSessionStore) metaPath(id string) string {
	return filepath.Join(s.sessionDir(id), "session.json")
}

func (s *FileSessionStore) historyPath(id string) string {
	return filepath.Join(s.sessionDir(id), "history.jsonl")
}

func (s *FileSessionStore) eventsPath(id string) string {
	return filepath.Join(s.sessionDir(id), "events.jsonl")
}

func (s *FileSessionStore) recordPath(id, namespace string) string {
	return filepath.Join(s.sessionDir(id), "state", namespace+".json")
}

// OpenEventSink opens the append-only actor event log for a session.
func (s *FileSessionStore) OpenEventSink(_ context.Context, id string) (actorsink.EventSink, error) {
	eventMu := s.eventLock(id)
	eventMu.Lock()
	defer eventMu.Unlock()
	if err := os.MkdirAll(s.sessionDir(id), 0o755); err != nil {
		return nil, err
	}
	release, err := tryAcquireEventWriterLock(filepath.Join(s.sessionDir(id), ".events.lock"))
	if err != nil {
		return nil, err
	}
	if err := repairEventLogTail(s.eventsPath(id)); err != nil {
		release()
		return nil, err
	}
	if err := actorsink.CompactJSONL(s.eventsPath(id), maxEventLogBytes, targetEventLogBytes); err != nil {
		release()
		return nil, err
	}
	eventSink, err := actorsink.NewBoundedJSONL(s.eventsPath(id), maxEventLogBytes, targetEventLogBytes)
	if err != nil {
		release()
		return nil, err
	}
	if err := os.Chmod(s.eventsPath(id), 0o600); err != nil {
		_ = eventSink.Close()
		release()
		return nil, err
	}
	return &lockedEventSink{mu: eventMu, inner: eventSink, release: release}, nil
}

type lockedEventSink struct {
	mu        *sync.Mutex
	inner     actorsink.EventSink
	release   func()
	closeOnce sync.Once
	closeErr  error
}

func (s *lockedEventSink) Append(ctx context.Context, evt events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.Append(ctx, evt)
}

func (s *lockedEventSink) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closeErr = s.inner.Close()
		s.release()
	})
	return s.closeErr
}

func (s *FileSessionStore) eventLock(id string) *sync.Mutex {
	lock, _ := s.eventLocks.LoadOrStore(id, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func repairEventLogTail(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}

	last := []byte{0}
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	lastNewline, err := previousNewline(f, info.Size()-1)
	if err != nil {
		return err
	}
	if last[0] != '\n' {
		// A JSONL record is committed only once its terminating newline exists.
		return f.Truncate(lastNewline + 1)
	}
	lineStart, err := previousNewline(f, lastNewline-1)
	if err != nil {
		return err
	}
	lineStart++
	line := make([]byte, lastNewline-lineStart)
	if _, err := f.ReadAt(line, lineStart); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	var evt events.Event
	if len(line) > 0 && json.Unmarshal(line, &evt) == nil {
		return nil
	}
	// Only repair the tail. Earlier malformed records remain visible to
	// LoadEvents as corruption rather than being silently discarded.
	return f.Truncate(lineStart)
}

func previousNewline(f *os.File, before int64) (int64, error) {
	const chunkSize int64 = 32 << 10
	for end := before; end >= 0; {
		start := end - chunkSize + 1
		if start < 0 {
			start = 0
		}
		buf := make([]byte, end-start+1)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return -1, err
		}
		for i := len(buf) - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				return start + int64(i), nil
			}
		}
		end = start - 1
	}
	return -1, nil
}

// LoadEvents replays valid physical JSONL records without modifying the log.
// Corrupt records are skipped so later durable events remain available.
func (s *FileSessionStore) LoadEvents(ctx context.Context, id string) ([]events.Event, error) {
	eventMu := s.eventLock(id)
	eventMu.Lock()
	defer eventMu.Unlock()
	f, err := os.Open(s.eventsPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var (
		out         []events.Event
		skipped     int
		lineNumber  int
		offset      int64
		firstLine   int
		firstOffset int64
		firstErr    error
	)
	defer func() {
		if skipped > 0 {
			corelogger.New("session.events").Warnw(
				"skipped malformed session event records",
				"session_id", id,
				"skipped_records", skipped,
				"first_line", firstLine,
				"first_offset", firstOffset,
				"first_error", firstErr,
			)
		}
	}()

	reader := bufio.NewReader(f)
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		recordOffset := offset
		line, readErr := reader.ReadBytes('\n')
		offset += int64(len(line))
		if len(line) > 0 {
			lineNumber++
			record := bytes.TrimSpace(line)
			if len(record) > 0 {
				var evt events.Event
				if err := json.Unmarshal(record, &evt); err != nil {
					skipped++
					if firstErr == nil {
						firstLine = lineNumber
						firstOffset = recordOffset
						firstErr = err
					}
				} else {
					out = append(out, evt)
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return out, nil
			}
			return out, readErr
		}
	}
}

func (s *FileSessionStore) sessionMemoryPath(id string) string {
	return filepath.Join(s.sessionDir(id), "session_memory.json")
}

func (s *FileSessionStore) plansDir(id string) string {
	return filepath.Join(s.sessionDir(id), "plans")
}

func (s *FileSessionStore) relationsDir(rootID string) string {
	return filepath.Join(s.sessionDir(rootID), "related")
}

func relationKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", sum[:])
}

func (s *FileSessionStore) relationPath(rootID, key string) string {
	return filepath.Join(s.relationsDir(rootID), relationKeyHash(key)+".json")
}

func (s *FileSessionStore) relationLockPath(rootID, key string) string {
	return filepath.Join(s.relationsDir(rootID), ".locks", relationKeyHash(key)+".lock")
}

func (s *FileSessionStore) planPath(id, planID string) string {
	return filepath.Join(s.plansDir(id), planID+".json")
}

// Store interface implementation

func (s *FileSessionStore) Create(sessionID string, llm providers.Client, opts ...coresession.Option) (*coresession.Session, error) {
	if err := s.EnsureDir(); err != nil {
		return nil, err
	}

	// Create session directory
	sessionDir := s.sessionDir(sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, err
	}

	now := time.Now()
	meta := sessions.SessionMeta{
		ID:           sessionID,
		CreatedAt:    now,
		UpdatedAt:    now,
		MessageCount: 0,
	}

	if err := writeJSONAtomic(s.metaPath(sessionID), meta, 0o644); err != nil {
		return nil, err
	}

	if err := os.WriteFile(s.historyPath(sessionID), []byte{}, 0644); err != nil {
		return nil, err
	}

	sess := coresession.New(sessionID, llm, append(opts,
		coresession.WithMessageWriter(s),
		coresession.WithRecordStore(s),
	)...)
	return sess, nil
}

func (s *FileSessionStore) Load(sessionID string, llm providers.Client, opts ...coresession.Option) (*coresession.Session, error) {
	metaPath := s.metaPath(sessionID)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s: %w", sessionID, err)
		}
		return nil, err
	}

	var meta sessions.SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}

	messages, err := s.LoadMessages(sessionID)
	if err != nil {
		return nil, err
	}

	sess := coresession.New(sessionID, llm, append(opts,
		coresession.WithHistory(messages...),
		coresession.WithMessageWriter(s),
		coresession.WithRecordStore(s),
	)...)

	return sess, nil
}

func (s *FileSessionStore) Delete(sessionID string) error {
	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	sessionDir := s.sessionDir(sessionID)
	return os.RemoveAll(sessionDir)
}

// ReadSessionRecord loads namespaced auxiliary state stored beside the
// session history.
func (s *FileSessionStore) ReadSessionRecord(ctx context.Context, sessionID, namespace string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validRecordNamespace(namespace) {
		return nil, fmt.Errorf("invalid session record namespace %q", namespace)
	}

	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	data, err := os.ReadFile(s.recordPath(sessionID, namespace))
	if os.IsNotExist(err) {
		return nil, coresession.ErrRecordNotFound
	}
	return data, err
}

// UpdateSessionRecord applies one serialized read-modify-write update.
func (s *FileSessionStore) UpdateSessionRecord(ctx context.Context, sessionID, namespace string, update func([]byte) ([]byte, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validRecordNamespace(namespace) {
		return fmt.Errorf("invalid session record namespace %q", namespace)
	}
	if update == nil {
		return errors.New("session record update is required")
	}

	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return err
	}
	defer unlock()

	current, err := os.ReadFile(s.recordPath(sessionID, namespace))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	next, err := update(current)
	if err != nil {
		return err
	}
	return writeBytesAtomic(s.recordPath(sessionID, namespace), next, 0o600)
}

func (s *FileSessionStore) List() ([]sessions.SessionMeta, error) {
	return s.listFiltered(false)
}

func (s *FileSessionStore) ListActive() ([]sessions.SessionMeta, error) {
	return s.listFiltered(true)
}

func (s *FileSessionStore) listFiltered(activeOnly bool) ([]sessions.SessionMeta, error) {
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []sessions.SessionMeta{}, nil
		}
		return nil, err
	}

	var result []sessions.SessionMeta
	for _, entry := range entries {
		// Only process directories
		if !entry.IsDir() {
			continue
		}

		sessionID := entry.Name()
		metaPath := s.metaPath(sessionID)

		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var meta sessions.SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}

		// Filter archived sessions for ListActive
		if activeOnly && meta.Archived {
			continue
		}
		result = append(result, meta)
	}

	return result, nil
}

func (s *FileSessionStore) GetMeta(sessionID string) (*sessions.SessionMeta, error) {
	return s.loadMeta(sessionID)
}

func (s *FileSessionStore) UpdateAlias(sessionID, alias string) error {
	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	meta.Alias = alias
	return s.saveMeta(sessionID, meta)
}

func (s *FileSessionStore) UpdateMeta(sessionID string, patch sessions.SessionMetaPatch) error {
	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	if patch.Name != nil {
		meta.Name = *patch.Name
	}
	if patch.Archived != nil {
		meta.Archived = *patch.Archived
	}
	if patch.Runtime != nil {
		meta.Runtime = *patch.Runtime
	}
	if patch.Mode != nil {
		meta.Runtime.Mode = *patch.Mode
	}
	if patch.Model != nil {
		meta.Runtime.Model = *patch.Model
	}
	if patch.Effort != nil {
		meta.Runtime.Effort = *patch.Effort
	}
	if patch.LatestPlanID != nil {
		meta.LatestPlanID = *patch.LatestPlanID
	}
	meta.UpdatedAt = time.Now()
	return s.saveMeta(sessionID, meta)
}

func (s *FileSessionStore) Archive(sessionID string) error {
	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	meta.Archived = true
	return s.saveMeta(sessionID, meta)
}

func (s *FileSessionStore) Unarchive(sessionID string) error {
	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	meta.Archived = false
	return s.saveMeta(sessionID, meta)
}

// AcquireRelationLock serializes relation creation across goroutines and
// processes. Callers must release the returned lock.
func (s *FileSessionStore) AcquireRelationLock(rootID, key string) (func(), error) {
	lockKey := rootID + "\x00" + key
	value, _ := s.relationLocks.LoadOrStore(lockKey, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	unlockFile, err := acquireMetadataFileLock(s.relationLockPath(rootID, key))
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return func() {
		unlockFile()
		mu.Unlock()
	}, nil
}

func (s *FileSessionStore) GetRelation(rootID, key string) (*sessions.Relation, error) {
	data, err := os.ReadFile(s.relationPath(rootID, key))
	if err != nil {
		return nil, err
	}
	var relation sessions.Relation
	if err := json.Unmarshal(data, &relation); err != nil {
		return nil, err
	}
	if relation.Version != 1 || relation.RootID != rootID || relation.Key != key || !validRelationSessionID(relation.SessionID) || relation.SessionID == rootID || relation.Kind != sessions.RelationKindAssociated {
		return nil, fmt.Errorf("session relation identity mismatch")
	}
	return &relation, nil
}

func (s *FileSessionStore) PutRelation(relation sessions.Relation) error {
	if relation.Version != 1 || relation.RootID == "" || relation.Key == "" || !validRelationSessionID(relation.SessionID) || relation.SessionID == relation.RootID || relation.Kind != sessions.RelationKindAssociated {
		return fmt.Errorf("invalid session relation")
	}
	if err := os.MkdirAll(s.relationsDir(relation.RootID), 0o755); err != nil {
		return err
	}
	return writeJSONAtomic(s.relationPath(relation.RootID, relation.Key), relation, 0o600)
}

func (s *FileSessionStore) DeleteRelation(rootID, key string) error {
	err := os.Remove(s.relationPath(rootID, key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *FileSessionStore) ListRelations(rootID string) ([]sessions.Relation, error) {
	entries, err := os.ReadDir(s.relationsDir(rootID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	result := make([]sessions.Relation, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.relationsDir(rootID), entry.Name()))
		if err != nil {
			return nil, err
		}
		var relation sessions.Relation
		if err := json.Unmarshal(data, &relation); err != nil {
			return nil, err
		}
		if relation.Version != 1 || relation.RootID != rootID || relation.Key == "" || !validRelationSessionID(relation.SessionID) || relation.SessionID == rootID || relation.Kind != sessions.RelationKindAssociated {
			return nil, fmt.Errorf("session relation identity mismatch")
		}
		result = append(result, relation)
	}
	return result, nil
}

func validRelationSessionID(id string) bool {
	return id != "" && id == strings.TrimSpace(id) && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

func (s *FileSessionStore) AppendMessages(sessionID string, msgs ...types.Message) error {
	historyPath := s.historyPath(sessionID)

	file, err := os.OpenFile(historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	appendedCount := 0
	for _, msg := range msgs {
		data, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			continue
		}
		appendedCount++
	}

	if appendedCount > 0 {
		return s.updateMeta(sessionID, appendedCount)
	}

	return nil
}

func (s *FileSessionStore) UpdateMessageTokens(sessionID string, updates map[int]int64) error {
	if len(updates) == 0 {
		return nil
	}

	msgs, err := s.LoadMessages(sessionID)
	if err != nil {
		return err
	}

	changed := false
	for idx, tokens := range updates {
		if idx < 0 || idx >= len(msgs) {
			continue
		}
		if msgs[idx].Tokens == tokens {
			continue
		}
		msgs[idx].Tokens = tokens
		changed = true
	}
	if !changed {
		return nil
	}

	return s.writeHistory(s.historyPath(sessionID), msgs)
}

func (s *FileSessionStore) LoadMessages(sessionID string) ([]types.Message, error) {
	historyPath := s.historyPath(sessionID)

	data, err := os.ReadFile(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []types.Message{}, nil
		}
		return nil, err
	}

	var messages []types.Message
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg types.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

func (s *FileSessionStore) ReplaceMessages(sessionID string, msgs ...types.Message) error {
	historyPath := s.historyPath(sessionID)

	// 1. Backup original file if exists
	if _, err := os.Stat(historyPath); err == nil {
		timestamp := time.Now().Format("20060102_150405")
		backupPath := filepath.Join(s.sessionDir(sessionID), fmt.Sprintf("history_origin_%s.jsonl", timestamp))
		if err := os.Rename(historyPath, backupPath); err != nil {
			return fmt.Errorf("failed to backup history: %w", err)
		}
	}

	// 2. Write new content
	if err := s.writeHistory(historyPath, msgs); err != nil {
		return err
	}

	// 3. Update metadata
	return s.updateMetaCount(sessionID, len(msgs))
}

func (s *FileSessionStore) writeHistory(historyPath string, msgs []types.Message) error {
	file, err := os.OpenFile(historyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, msg := range msgs {
		data, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileSessionStore) WriteSessionMemory(sessionID string, record *contextmgr.SessionMemoryRecord) error {
	if record == nil {
		return nil
	}

	if err := os.MkdirAll(s.sessionDir(sessionID), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.sessionMemoryPath(sessionID), data, 0644)
}

func (s *FileSessionStore) ReadSessionMemory(sessionID string) (*contextmgr.SessionMemoryRecord, error) {
	data, err := os.ReadFile(s.sessionMemoryPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var record contextmgr.SessionMemoryRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *FileSessionStore) SavePlan(sessionID string, plan planning.Artifact) error {
	if err := validatePlan(sessionID, plan, true); err != nil {
		return err
	}
	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.plansDir(sessionID), 0o755); err != nil {
		return err
	}
	if err := writeJSONAtomic(s.planPath(sessionID, plan.ID), plan, 0o600); err != nil {
		return err
	}
	if meta.LatestPlanID != "" {
		return nil
	}
	meta.LatestPlanID = plan.ID
	meta.UpdatedAt = time.Now()
	return s.saveMeta(sessionID, meta)
}

func (s *FileSessionStore) ProposePlan(sessionID string, plan planning.Artifact) (*planning.Artifact, error) {
	plan.Status = planning.ArtifactProposed
	if err := validatePlan(sessionID, plan, false); err != nil {
		return nil, err
	}
	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return nil, err
	}
	plan.Version = 1
	if meta.LatestPlanID != "" {
		previous, err := s.loadPlanFile(sessionID, meta.LatestPlanID)
		if err != nil {
			return nil, fmt.Errorf("load latest plan: %w", err)
		}
		plan.Version = previous.Version + 1
	}
	if err := os.MkdirAll(s.plansDir(sessionID), 0o755); err != nil {
		return nil, err
	}
	if err := writeJSONAtomic(s.planPath(sessionID, plan.ID), plan, 0o600); err != nil {
		return nil, err
	}
	// Commit the latest pointer after the artifact exists. A crash between the
	// two writes leaves only an orphan artifact; the previous plan remains the
	// authoritative latest version and no actionable plan is lost.
	meta.LatestPlanID = plan.ID
	meta.UpdatedAt = time.Now()
	if err := s.saveMeta(sessionID, meta); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *FileSessionStore) LoadPlan(sessionID, planID string) (*planning.Artifact, error) {
	if !validPlanID(planID) {
		return nil, fmt.Errorf("invalid plan id")
	}
	plan, err := s.loadPlanFile(sessionID, planID)
	if err != nil {
		return nil, err
	}
	if meta, metaErr := s.loadMeta(sessionID); metaErr == nil && meta.LatestPlanID != planID && plan.Status == planning.ArtifactProposed {
		plan.Status = planning.ArtifactSuperseded
	}
	return plan, nil
}

func (s *FileSessionStore) loadPlanFile(sessionID, planID string) (*planning.Artifact, error) {
	data, err := os.ReadFile(s.planPath(sessionID, planID))
	if err != nil {
		return nil, err
	}
	var plan planning.Artifact
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	if plan.ID != planID || plan.SessionID != sessionID || plan.Version < 1 || !validPlanStatus(plan.Status) {
		return nil, fmt.Errorf("plan artifact identity mismatch")
	}
	return &plan, nil
}

func validatePlan(sessionID string, plan planning.Artifact, requireVersion bool) error {
	if !validPlanID(plan.ID) || plan.SessionID != sessionID || (requireVersion && plan.Version < 1) || strings.TrimSpace(plan.Title) == "" || strings.TrimSpace(plan.Markdown) == "" || !validPlanStatus(plan.Status) {
		return fmt.Errorf("invalid plan artifact")
	}
	return nil
}

func validPlanID(id string) bool {
	return id != "" && id == strings.TrimSpace(id) && !strings.ContainsAny(id, `/\`) && id != "." && id != ".."
}

func validRecordNamespace(namespace string) bool {
	return validPlanID(namespace)
}

func validPlanStatus(status planning.ArtifactStatus) bool {
	switch status {
	case planning.ArtifactProposed, planning.ArtifactAccepted, planning.ArtifactSuperseded:
		return true
	default:
		return false
	}
}

func (s *FileSessionStore) LoadLatestPlan(sessionID string) (*planning.Artifact, error) {
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return nil, err
	}
	if meta.LatestPlanID == "" {
		return nil, nil
	}
	return s.LoadPlan(sessionID, meta.LatestPlanID)
}

func (s *FileSessionStore) updateMetaCount(sessionID string, count int) error {
	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	meta.UpdatedAt = time.Now()
	meta.MessageCount = count
	return s.saveMeta(sessionID, meta)
}

func (s *FileSessionStore) updateMeta(sessionID string, added int) error {
	unlock, err := s.lockMeta(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	metaPath := s.metaPath(sessionID)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}

	var meta sessions.SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}

	meta.UpdatedAt = time.Now()
	meta.MessageCount += added

	return s.saveMeta(sessionID, &meta)
}

func (s *FileSessionStore) lockMeta(sessionID string) (func(), error) {
	value, _ := s.metaLocks.LoadOrStore(sessionID, &sync.Mutex{})
	local := value.(*sync.Mutex)
	local.Lock()
	releaseFile, err := acquireMetadataFileLock(filepath.Join(s.sessionDir(sessionID), ".session.lock"))
	if err != nil {
		local.Unlock()
		return nil, err
	}
	return func() {
		releaseFile()
		local.Unlock()
	}, nil
}

func (s *FileSessionStore) loadMeta(sessionID string) (*sessions.SessionMeta, error) {
	metaPath := s.metaPath(sessionID)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s: %w", sessionID, err)
		}
		return nil, err
	}
	var meta sessions.SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (s *FileSessionStore) saveMeta(sessionID string, meta *sessions.SessionMeta) error {
	metaPath := s.metaPath(sessionID)
	return writeJSONAtomic(metaPath, meta, 0o644)
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBytesAtomic(path, data, mode)
}

func writeBytesAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".friday-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
