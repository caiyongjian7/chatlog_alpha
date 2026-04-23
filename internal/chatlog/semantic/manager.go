package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/chatlog/internal/chatlog/conf"
	"github.com/sjzar/chatlog/internal/model"
	"github.com/sjzar/chatlog/internal/wechatdb"
)

type ConfigProvider interface {
	GetWorkDir() string
	GetSemanticConfig() *conf.SemanticConfig
}

type DBSource interface {
	GetSessions(key string, limit, offset int) (*wechatdb.GetSessionsResp, error)
	GetMessages(start, end time.Time, talker string, sender string, keyword string, limit, offset int) ([]*model.Message, error)
	GetContact(key string) (*model.Contact, error)
	GetChatRoom(key string) (*model.ChatRoom, error)
}

type SearchHit struct {
	Talker      string  `json:"talker"`
	TalkerName  string  `json:"talker_name"`
	Sender      string  `json:"sender"`
	SenderName  string  `json:"sender_name"`
	Seq         int64   `json:"seq"`
	Time        int64   `json:"time"`
	Type        int64   `json:"type"`
	SubType     int64   `json:"sub_type"`
	Content     string  `json:"content"`
	Score       float64 `json:"score"`
	RerankScore float64 `json:"rerank_score,omitempty"`
}

type IndexStatus struct {
	Ready                bool    `json:"ready"`
	Enabled              bool    `json:"enabled"`
	StorePath            string  `json:"store_path"`
	Running              bool    `json:"running"`
	Mode                 string  `json:"mode"`
	IndexedCount         int     `json:"indexed_count"`
	Processed            int     `json:"processed"` // successfully completed talkers
	Failed               int     `json:"failed"`
	Pending              int     `json:"pending"`
	Total                int     `json:"total"`
	ProgressPct          float64 `json:"progress_pct"`
	UpdatedAt            string  `json:"updated_at,omitempty"`
	LastError            string  `json:"last_error,omitempty"`
	LastIncrementalAt    string  `json:"last_incremental_at,omitempty"`
	LastIncrementalAdded int     `json:"last_incremental_added"`
	LastIncrementalError string  `json:"last_incremental_error,omitempty"`
	LastRerankAt         string  `json:"last_rerank_at,omitempty"`
	LastRerankApplied    bool    `json:"last_rerank_applied"`
	LastRerankError      string  `json:"last_rerank_error,omitempty"`
}

type indexCheckpoint struct {
	Mode      string           `json:"mode"`
	Model     string           `json:"model"`
	Dim       int              `json:"dim"`
	StartedAt string           `json:"started_at"`
	UpdatedAt string           `json:"updated_at"`
	Total     int              `json:"total"`
	Completed map[string]int64 `json:"completed"`
}

type Manager struct {
	conf   ConfigProvider
	db     DBSource
	client *Client
	store  *Store

	mu     sync.RWMutex
	status IndexStatus
}

func NewManager(conf ConfigProvider, db DBSource) (*Manager, error) {
	store, err := OpenStore(conf.GetWorkDir())
	if err != nil {
		return nil, err
	}
	m := &Manager{
		conf:   conf,
		db:     db,
		client: NewClient(),
		store:  store,
		status: IndexStatus{
			Ready:     true,
			StorePath: store.Path(),
		},
	}
	_ = m.refreshCount()
	return m, nil
}

func (m *Manager) Close() error {
	if m == nil || m.store == nil {
		return nil
	}
	return m.store.Close()
}

func (m *Manager) TestConnection(ctx context.Context, cfg conf.SemanticConfig) error {
	cfg = conf.NormalizeSemanticConfig(cfg)
	return m.client.Test(ctx, cfg)
}

func (m *Manager) Status() IndexStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := m.status
	out.Enabled = m.isEnabled()
	return out
}

func (m *Manager) Rebuild(ctx context.Context, reset bool) error {
	cfg := m.currentConfig()
	if !cfg.Enabled {
		return fmt.Errorf("semantic is disabled")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("glm api key is empty")
	}
	if err := m.withBuildStatus("rebuild", func() error {
		return m.buildAll(ctx, cfg, "rebuild", true, reset)
	}); err != nil {
		return err
	}
	return m.refreshCount()
}

func (m *Manager) Incremental(ctx context.Context) error {
	cfg := m.currentConfig()
	if !cfg.Enabled || !cfg.RealtimeIndex {
		return nil
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil
	}
	if m.Status().Running {
		return nil
	}
	before, _ := m.store.Count()
	if err := m.withBuildStatus("incremental", func() error {
		return m.buildAll(ctx, cfg, "incremental", false, false)
	}); err != nil {
		m.mu.Lock()
		m.status.LastIncrementalAt = time.Now().Format(time.RFC3339)
		m.status.LastIncrementalError = err.Error()
		m.status.LastIncrementalAdded = 0
		m.mu.Unlock()
		return err
	}
	after, _ := m.store.Count()
	added := after - before
	if added < 0 {
		added = 0
	}
	m.mu.Lock()
	m.status.LastIncrementalAt = time.Now().Format(time.RFC3339)
	m.status.LastIncrementalAdded = added
	m.status.LastIncrementalError = ""
	m.mu.Unlock()
	return m.refreshCount()
}

func (m *Manager) Clear() error {
	if err := m.store.Clear(); err != nil {
		return err
	}
	_ = m.store.DeleteMeta(m.checkpointMetaKey("rebuild", m.currentConfig()))
	return m.refreshCount()
}

func (m *Manager) Search(ctx context.Context, query, talker string, limit int, rerank bool) ([]SearchHit, error) {
	cfg := m.currentConfig()
	if !cfg.Enabled {
		return nil, fmt.Errorf("semantic is disabled")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if limit <= 0 {
		limit = cfg.TopN
	}
	if limit <= 0 {
		limit = 20
	}
	if err := m.Incremental(ctx); err != nil {
		log.Debug().Err(err).Msg("semantic incremental indexing failed")
	}

	vecs, err := m.client.Embed(ctx, cfg, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, nil
	}

	recall := cfg.RecallK
	if recall < limit {
		recall = limit
	}
	records, err := m.store.LoadCandidates(strings.TrimSpace(talker), cfg.EmbeddingModel, cfg.EmbeddingDimension, maxInt(recall*8, 800))
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	scored := make([]SearchHit, 0, len(records))
	for _, item := range records {
		score := cosine(vecs[0], item.Vector)
		if score < cfg.SimilarityThreshold {
			continue
		}
		talkerName := item.Talker
		senderName := item.Sender
		if contact, _ := m.db.GetContact(item.Talker); contact != nil && strings.TrimSpace(contact.NickName) != "" {
			talkerName = contact.NickName
		} else if room, _ := m.db.GetChatRoom(item.Talker); room != nil && strings.TrimSpace(room.NickName) != "" {
			talkerName = room.NickName
		}
		if contact, _ := m.db.GetContact(item.Sender); contact != nil && strings.TrimSpace(contact.NickName) != "" {
			senderName = contact.NickName
		}
		scored = append(scored, SearchHit{
			Talker:     item.Talker,
			TalkerName: talkerName,
			Sender:     item.Sender,
			SenderName: senderName,
			Seq:        item.Seq,
			Time:       item.TS,
			Type:       item.Type,
			SubType:    item.SubType,
			Content:    item.Content,
			Score:      score,
		})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > recall {
		scored = scored[:recall]
	}
	if rerank && cfg.EnableRerank {
		docs := make([]string, 0, len(scored))
		for _, item := range scored {
			docs = append(docs, item.Content)
		}
		rank, err := m.client.Rerank(ctx, cfg, query, docs, minInt(limit, len(docs)))
		if err == nil && len(rank) > 0 {
			ranked := make([]SearchHit, 0, len(rank))
			for _, item := range rank {
				if item.Index < 0 || item.Index >= len(scored) {
					continue
				}
				h := scored[item.Index]
				h.RerankScore = item.Score
				ranked = append(ranked, h)
			}
			if len(ranked) > 0 {
				scored = ranked
				m.mu.Lock()
				m.status.LastRerankAt = time.Now().Format(time.RFC3339)
				m.status.LastRerankApplied = true
				m.status.LastRerankError = ""
				m.mu.Unlock()
			}
		} else if err != nil {
			m.mu.Lock()
			m.status.LastRerankAt = time.Now().Format(time.RFC3339)
			m.status.LastRerankApplied = false
			m.status.LastRerankError = err.Error()
			m.mu.Unlock()
		}
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

func (m *Manager) MatchKeywordSemantically(ctx context.Context, content string, keywords []string) (string, float64, error) {
	cfg := m.currentConfig()
	if !cfg.Enabled || !cfg.EnableSemanticPush || len(keywords) == 0 {
		return "", 0, nil
	}
	query := strings.TrimSpace(content)
	if query == "" {
		return "", 0, nil
	}
	rank, err := m.client.Rerank(ctx, cfg, query, keywords, 1)
	if err != nil || len(rank) == 0 {
		return "", 0, err
	}
	item := rank[0]
	if item.Index < 0 || item.Index >= len(keywords) {
		return "", 0, nil
	}
	if item.Score < cfg.SimilarityThreshold {
		return "", item.Score, nil
	}
	return keywords[item.Index], item.Score, nil
}

func (m *Manager) buildAll(ctx context.Context, cfg conf.SemanticConfig, mode string, full, reset bool) error {
	sessions, err := m.db.GetSessions("", 5000, 0)
	if err != nil {
		return err
	}
	if sessions == nil || len(sessions.Items) == 0 {
		return nil
	}

	talkers := make([]string, 0, len(sessions.Items))
	seen := map[string]struct{}{}
	for _, sess := range sessions.Items {
		if sess == nil {
			continue
		}
		talker := strings.TrimSpace(sess.UserName)
		if talker == "" {
			continue
		}
		if _, ok := seen[talker]; ok {
			continue
		}
		seen[talker] = struct{}{}
		talkers = append(talkers, talker)
	}
	if len(talkers) == 0 {
		return nil
	}

	var cp *indexCheckpoint
	if mode == "rebuild" {
		cp, err = m.loadCheckpoint(mode, cfg)
		if err != nil {
			log.Debug().Err(err).Msg("load checkpoint failed")
		}
		if reset || cp == nil {
			if err := m.store.Clear(); err != nil {
				return err
			}
			cp = &indexCheckpoint{
				Mode:      mode,
				Model:     cfg.EmbeddingModel,
				Dim:       cfg.EmbeddingDimension,
				StartedAt: time.Now().Format(time.RFC3339),
				UpdatedAt: time.Now().Format(time.RFC3339),
				Total:     len(talkers),
				Completed: map[string]int64{},
			}
			if err := m.saveCheckpoint(mode, cfg, cp); err != nil {
				log.Debug().Err(err).Msg("save checkpoint failed")
			}
		}
		if cp.Completed == nil {
			cp.Completed = map[string]int64{}
		}
	}

	type task struct {
		talker   string
		startSeq int64
	}

	tasks := make([]task, 0, len(talkers))
	processed := 0
	for _, talker := range talkers {
		startSeq := int64(0)
		if full {
			if cp != nil {
				if seq, ok := cp.Completed[talker]; ok {
					processed++
					_ = seq
					continue
				}
			}
		} else {
			maxSeq, err := m.store.MaxSeq(talker, cfg.EmbeddingModel, cfg.EmbeddingDimension)
			if err == nil && maxSeq > 0 {
				startSeq = maxSeq
			}
		}
		tasks = append(tasks, task{talker: talker, startSeq: startSeq})
	}

	total := len(talkers)
	m.setProgress(processed, 0, total)

	workerN := cfg.IndexWorkers
	if workerN <= 0 {
		workerN = conf.DefaultSemanticWorkers
	}
	if workerN > total {
		workerN = total
	}
	if workerN <= 0 {
		workerN = 1
	}

	taskCh := make(chan task, total)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	var doneMu sync.Mutex
	failed := 0

	recordDone := func(talker string, seq int64, success bool) {
		doneMu.Lock()
		defer doneMu.Unlock()
		if success {
			processed++
		} else {
			failed++
		}
		m.setProgress(processed, failed, total)
		if cp != nil && success {
			if seq > 0 {
				cp.Completed[talker] = seq
			} else if _, ok := cp.Completed[talker]; !ok {
				cp.Completed[talker] = 0
			}
			cp.UpdatedAt = time.Now().Format(time.RFC3339)
			_ = m.saveCheckpoint(mode, cfg, cp)
		}
		_ = m.refreshCount()
	}

	for i := 0; i < workerN; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				select {
				case <-ctx.Done():
					errMu.Lock()
					if firstErr == nil {
						firstErr = ctx.Err()
					}
					errMu.Unlock()
					return
				default:
				}
				lastSeq, err := m.buildTalkerFromSeq(ctx, cfg, t.talker, t.startSeq)
				if err != nil {
					log.Debug().Err(err).Str("talker", t.talker).Msg("semantic build talker failed")
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					recordDone(t.talker, lastSeq, false)
					// Continue other talkers; failed talker remains pending for next resume.
					continue
				}
				recordDone(t.talker, lastSeq, true)
			}
		}()
	}

	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)
	wg.Wait()

	if cp != nil && processed >= total {
		_ = m.store.DeleteMeta(m.checkpointMetaKey(mode, cfg))
	}
	if firstErr != nil {
		return firstErr
	}
	return nil
}

func (m *Manager) buildTalkerFromSeq(ctx context.Context, cfg conf.SemanticConfig, talker string, startSeq int64) (int64, error) {
	start := time.Time{}
	if startSeq > 0 {
		start = time.Unix(startSeq/1_000_000, 0)
	}
	msgs, err := m.db.GetMessages(start, time.Time{}, talker, "", "", 0, 0)
	if err != nil {
		return startSeq, err
	}
	if len(msgs) == 0 {
		return startSeq, nil
	}
	texts := make([]string, 0, len(msgs))
	src := make([]*model.Message, 0, len(msgs))
	lastSeq := startSeq
	for _, m0 := range msgs {
		if m0 == nil || m0.Seq <= startSeq {
			continue
		}
		text := normalizeMessageText(m0)
		if text == "" {
			if m0.Seq > lastSeq {
				lastSeq = m0.Seq
			}
			continue
		}
		texts = append(texts, text)
		src = append(src, m0)
		if m0.Seq > lastSeq {
			lastSeq = m0.Seq
		}
	}
	for i := 0; i < len(texts); i += 64 {
		end := minInt(i+64, len(texts))
		batch := texts[i:end]
		vecs, err := m.client.Embed(ctx, cfg, batch)
		if err != nil {
			return lastSeq, err
		}
		recs := make([]record, 0, len(vecs))
		for j, vec := range vecs {
			msg := src[i+j]
			recs = append(recs, record{
				Talker:  msg.Talker,
				Seq:     msg.Seq,
				Sender:  msg.Sender,
				IsSelf:  msg.IsSelf,
				Type:    msg.Type,
				SubType: msg.SubType,
				TS:      msg.Time.Unix(),
				Content: batch[j],
				Model:   cfg.EmbeddingModel,
				Dim:     len(vec),
				Vector:  vec,
			})
		}
		if err := m.store.Upsert(recs); err != nil {
			return lastSeq, err
		}
	}
	return lastSeq, nil
}

func (m *Manager) withBuildStatus(mode string, fn func() error) error {
	m.mu.Lock()
	if m.status.Running {
		m.mu.Unlock()
		return fmt.Errorf("index job is already running")
	}
	m.status.Running = true
	m.status.Mode = mode
	m.status.LastError = ""
	m.mu.Unlock()

	err := fn()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Running = false
	m.status.Mode = ""
	m.status.UpdatedAt = time.Now().Format(time.RFC3339)
	if err != nil {
		m.status.LastError = err.Error()
	}
	return err
}

func (m *Manager) setProgress(processed, failed, total int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Processed = processed
	m.status.Failed = failed
	m.status.Total = total
	pending := total - processed
	if pending < 0 {
		pending = 0
	}
	m.status.Pending = pending
	if total > 0 {
		m.status.ProgressPct = float64(processed) * 100 / float64(total)
		if m.status.ProgressPct < 0 {
			m.status.ProgressPct = 0
		}
		if m.status.ProgressPct > 100 {
			m.status.ProgressPct = 100
		}
	} else {
		m.status.ProgressPct = 0
	}
}

func (m *Manager) refreshCount() error {
	n, err := m.store.Count()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.status.IndexedCount = n
	m.mu.Unlock()
	return nil
}

func (m *Manager) checkpointMetaKey(mode string, cfg conf.SemanticConfig) string {
	return fmt.Sprintf("checkpoint:%s:%s:%d", strings.TrimSpace(mode), strings.TrimSpace(cfg.EmbeddingModel), cfg.EmbeddingDimension)
}

func (m *Manager) loadCheckpoint(mode string, cfg conf.SemanticConfig) (*indexCheckpoint, error) {
	key := m.checkpointMetaKey(mode, cfg)
	raw, err := m.store.GetMeta(key)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	var cp indexCheckpoint
	if err := json.Unmarshal([]byte(raw), &cp); err != nil {
		return nil, err
	}
	if cp.Completed == nil {
		cp.Completed = map[string]int64{}
	}
	return &cp, nil
}

func (m *Manager) saveCheckpoint(mode string, cfg conf.SemanticConfig, cp *indexCheckpoint) error {
	if cp == nil {
		return nil
	}
	cp.Mode = mode
	cp.Model = cfg.EmbeddingModel
	cp.Dim = cfg.EmbeddingDimension
	cp.UpdatedAt = time.Now().Format(time.RFC3339)
	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	return m.store.SaveMeta(m.checkpointMetaKey(mode, cfg), string(raw))
}

func (m *Manager) currentConfig() conf.SemanticConfig {
	cfg := conf.SemanticConfig{}
	if m.conf != nil && m.conf.GetSemanticConfig() != nil {
		cfg = *m.conf.GetSemanticConfig()
	}
	return conf.NormalizeSemanticConfig(cfg)
}

func (m *Manager) isEnabled() bool {
	return m.currentConfig().Enabled
}

func normalizeMessageText(m *model.Message) string {
	if m == nil {
		return ""
	}
	txt := strings.TrimSpace(m.PlainTextContent())
	if txt == "" {
		txt = strings.TrimSpace(m.Content)
	}
	if txt == "" {
		return ""
	}
	if len(txt) > 4000 {
		return txt[:4000]
	}
	return txt
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := minInt(len(a), len(b))
	var dot, normA, normB float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
