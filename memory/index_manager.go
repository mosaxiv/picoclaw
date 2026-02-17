package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mosaxiv/clawlet/config"
	_ "github.com/mosaxiv/clawlet/internal/sqlite3"
)

const (
	metaKeyMemoryIndex = "memory_index_meta_v1"
	vectorTableName    = "chunks_vec"
	ftsTableName       = "chunks_fts"
	cacheTableName     = "embedding_cache"
	snippetMaxChars    = 700
)

type SearchOptions struct {
	MaxResults int
	MinScore   float64
}

type SearchResult struct {
	Path      string  `json:"path"`
	StartLine int     `json:"startLine"`
	EndLine   int     `json:"endLine"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
}

type ReadFileOptions struct {
	From  int
	Lines int
}

type SearchStatus struct {
	Enabled       bool    `json:"enabled"`
	Provider      string  `json:"provider,omitempty"`
	Model         string  `json:"model,omitempty"`
	DBPath        string  `json:"dbPath,omitempty"`
	Files         int     `json:"files"`
	Chunks        int     `json:"chunks"`
	VectorEnabled bool    `json:"vectorEnabled"`
	VectorReady   bool    `json:"vectorReady"`
	VectorDims    int     `json:"vectorDims"`
	FTSEnabled    bool    `json:"ftsEnabled"`
	FTSReady      bool    `json:"ftsReady"`
	MinScore      float64 `json:"minScore"`
	MaxResults    int     `json:"maxResults"`
	LastError     string  `json:"lastError,omitempty"`
}

type SearchManager interface {
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
	ReadFile(relPath string, opts ReadFileOptions) (text string, resolvedPath string, err error)
	Sync(ctx context.Context, force bool) error
	Status(ctx context.Context) SearchStatus
	Close() error
}

type IndexManager struct {
	workspaceDir string
	cfg          resolvedSearchConfig
	provider     *openAIEmbeddingProvider
	db           *sql.DB

	// Serialized via dbMu for predictable index consistency.
	dbMu sync.Mutex

	vectorReady bool
	vectorDims  int
	ftsReady    bool
	lastError   string
}

type resolvedSearchConfig struct {
	enabled bool

	provider string
	model    string

	baseURL string
	apiKey  string
	headers map[string]string

	storePath string

	vectorEnabled bool
	chunkTokens   int
	chunkOverlap  int

	maxResults int
	minScore   float64

	hybridVectorWeight float64
	hybridTextWeight   float64
	candidateMul       int

	cacheEnabled bool
	cacheMax     int

	syncOnSearch bool
}

type indexMeta struct {
	Model       string `json:"model"`
	Provider    string `json:"provider"`
	ProviderKey string `json:"providerKey"`
	ChunkTokens int    `json:"chunkTokens"`
	ChunkOver   int    `json:"chunkOverlap"`
	VectorDims  int    `json:"vectorDims,omitempty"`
}

type memoryFileEntry struct {
	AbsPath  string
	RelPath  string
	Hash     string
	Size     int64
	Modified int64
	Content  string
}

type chunkEntry struct {
	StartLine int
	EndLine   int
	Text      string
	Hash      string
}

type keywordResult struct {
	ID string
	SearchResult
	TextScore float64
}

type vectorResult struct {
	ID string
	SearchResult
	VectorScore float64
}

type openAIEmbeddingProvider struct {
	provider string
	baseURL  string
	apiKey   string
	model    string
	headers  map[string]string
	client   *http.Client
}

func NewIndexManager(cfg *config.Config, workspace string) (*IndexManager, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, errors.New("workspace is empty")
	}
	ws, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}

	resolved, err := resolveSearchConfig(cfg, ws)
	if err != nil {
		return nil, err
	}
	if !resolved.enabled {
		return nil, nil
	}

	if err := os.MkdirAll(filepath.Dir(resolved.storePath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", resolved.storePath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, err
	}

	m := &IndexManager{
		workspaceDir: ws,
		cfg:          resolved,
		db:           db,
		provider: &openAIEmbeddingProvider{
			provider: resolved.provider,
			baseURL:  strings.TrimRight(resolved.baseURL, "/"),
			apiKey:   resolved.apiKey,
			model:    resolved.model,
			headers:  copyHeaders(resolved.headers),
			client:   &http.Client{Timeout: 60 * time.Second},
		},
	}
	if err := m.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	meta, _ := m.readMeta()
	if meta != nil && meta.VectorDims > 0 {
		m.vectorDims = meta.VectorDims
		m.vectorReady = m.cfg.vectorEnabled
	}
	return m, nil
}

func (m *IndexManager) Close() error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.Close()
}

func (m *IndexManager) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if m == nil {
		return nil, errors.New("memory manager is nil")
	}
	cleaned := strings.TrimSpace(query)
	if cleaned == "" {
		return []SearchResult{}, nil
	}

	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = m.cfg.maxResults
	}
	minScore := opts.MinScore
	if minScore <= 0 {
		minScore = m.cfg.minScore
	}
	candidates := maxResults * m.cfg.candidateMul
	if candidates < 1 {
		candidates = maxResults
	}
	if candidates > 200 {
		candidates = 200
	}

	m.dbMu.Lock()
	defer m.dbMu.Unlock()

	if m.cfg.syncOnSearch {
		if err := m.syncLocked(ctx, false); err != nil {
			return nil, err
		}
	}

	queryVec, err := m.provider.EmbedBatch(ctx, []string{cleaned})
	if err != nil {
		m.lastError = err.Error()
		return nil, err
	}
	qv := []float64{}
	if len(queryVec) > 0 {
		qv = queryVec[0]
	}

	vectorRows, err := m.searchVectorLocked(qv, candidates)
	if err != nil {
		return nil, err
	}
	keywordRows, err := m.searchKeywordLocked(cleaned, candidates)
	if err != nil {
		return nil, err
	}
	merged := mergeHybrid(vectorRows, keywordRows, m.cfg.hybridVectorWeight, m.cfg.hybridTextWeight)
	return clampResults(merged, maxResults, minScore), nil
}

func (m *IndexManager) ReadFile(relPath string, opts ReadFileOptions) (string, string, error) {
	if m == nil {
		return "", "", errors.New("memory manager is nil")
	}
	raw := strings.TrimSpace(relPath)
	if raw == "" {
		return "", "", errors.New("path required")
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(m.workspaceDir, raw)
	}
	abs = filepath.Clean(abs)

	rp, err := filepath.Rel(m.workspaceDir, abs)
	if err != nil {
		return "", "", errors.New("path required")
	}
	rp = filepath.ToSlash(rp)
	if strings.HasPrefix(rp, "../") || rp == ".." || !isMemoryPath(rp) {
		return "", "", errors.New("path required")
	}
	if !strings.HasSuffix(strings.ToLower(rp), ".md") {
		return "", "", errors.New("path required")
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() || (info.Mode()&os.ModeSymlink) != 0 {
		return "", "", errors.New("path required")
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", "", err
	}
	content := string(b)
	if opts.From <= 0 && opts.Lines <= 0 {
		return content, rp, nil
	}
	lines := strings.Split(content, "\n")
	start := opts.From
	if start <= 0 {
		start = 1
	}
	count := opts.Lines
	if count <= 0 {
		count = len(lines)
	}
	from := min(start-1, len(lines))
	to := min(from+count, len(lines))
	return strings.Join(lines[from:to], "\n"), rp, nil
}

func (m *IndexManager) Sync(ctx context.Context, force bool) error {
	if m == nil {
		return errors.New("memory manager is nil")
	}
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	return m.syncLocked(ctx, force)
}

func (m *IndexManager) Status(ctx context.Context) SearchStatus {
	if m == nil {
		return SearchStatus{Enabled: false}
	}
	out := SearchStatus{
		Enabled:       m != nil,
		Provider:      m.cfg.provider,
		Model:         m.cfg.model,
		DBPath:        m.cfg.storePath,
		VectorEnabled: m.cfg.vectorEnabled,
		VectorReady:   m.vectorReady,
		VectorDims:    m.vectorDims,
		FTSEnabled:    true,
		FTSReady:      m.ftsReady,
		MinScore:      m.cfg.minScore,
		MaxResults:    m.cfg.maxResults,
		LastError:     m.lastError,
	}
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	out.Files = queryCount(m.db, `SELECT COUNT(*) FROM files`)
	out.Chunks = queryCount(m.db, `SELECT COUNT(*) FROM chunks`)
	return out
}

func (m *IndexManager) syncLocked(ctx context.Context, force bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.ensureSchema(); err != nil {
		return err
	}
	meta, err := m.readMeta()
	if err != nil {
		return err
	}
	providerKey := m.provider.providerKey()
	needFull := force || meta == nil ||
		meta.Model != m.cfg.model ||
		meta.Provider != m.cfg.provider ||
		meta.ProviderKey != providerKey ||
		meta.ChunkTokens != m.cfg.chunkTokens ||
		meta.ChunkOver != m.cfg.chunkOverlap
	if needFull {
		if err := m.resetIndexLocked(); err != nil {
			return err
		}
	}

	files, err := m.listMemoryFilesLocked()
	if err != nil {
		return err
	}
	active := make(map[string]struct{}, len(files))
	for _, f := range files {
		active[f.RelPath] = struct{}{}
	}

	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		var oldHash string
		err := m.db.QueryRow(`SELECT hash FROM files WHERE path = ?`, f.RelPath).Scan(&oldHash)
		if err == nil && !needFull && oldHash == f.Hash {
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := m.indexFileLocked(ctx, f); err != nil {
			return err
		}
	}

	rows, err := m.db.Query(`SELECT path FROM files`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return err
		}
		if _, ok := active[p]; !ok {
			stale = append(stale, p)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range stale {
		if err := m.deletePathLocked(p); err != nil {
			return err
		}
	}

	next := &indexMeta{
		Model:       m.cfg.model,
		Provider:    m.cfg.provider,
		ProviderKey: providerKey,
		ChunkTokens: m.cfg.chunkTokens,
		ChunkOver:   m.cfg.chunkOverlap,
		VectorDims:  m.vectorDims,
	}
	if err := m.writeMeta(next); err != nil {
		return err
	}
	if err := m.pruneEmbeddingCacheLocked(); err != nil {
		return err
	}
	return nil
}

func (m *IndexManager) ensureSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			hash TEXT NOT NULL,
			mtime INTEGER NOT NULL,
			size INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			hash TEXT NOT NULL,
			model TEXT NOT NULL,
			text TEXT NOT NULL,
			embedding TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			provider_key TEXT NOT NULL,
			hash TEXT NOT NULL,
			embedding TEXT NOT NULL,
			dims INTEGER,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (provider, model, provider_key, hash)
		)`, cacheTableName),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_updated_at ON %s(updated_at)`, cacheTableName, cacheTableName),
		`CREATE INDEX IF NOT EXISTS idx_chunks_path ON chunks(path)`,
	}
	for _, s := range stmts {
		if _, err := m.db.Exec(s); err != nil {
			return err
		}
	}

	m.ftsReady = false
	if _, err := m.db.Exec(
		`CREATE VIRTUAL TABLE IF NOT EXISTS ` + ftsTableName + ` USING fts5(
			text,
			id UNINDEXED,
			path UNINDEXED,
			model UNINDEXED,
			start_line UNINDEXED,
			end_line UNINDEXED
		)`,
	); err != nil {
		m.lastError = err.Error()
		return err
	}
	m.ftsReady = true
	return nil
}

func (m *IndexManager) readMeta() (*indexMeta, error) {
	var raw string
	err := m.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, metaKeyMemoryIndex).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out indexMeta
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, nil
	}
	return &out, nil
}

func (m *IndexManager) writeMeta(meta *indexMeta) error {
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = m.db.Exec(
		`INSERT INTO meta(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		metaKeyMemoryIndex,
		string(b),
	)
	return err
}

func (m *IndexManager) ensureVectorTableLocked(dims int) error {
	if !m.cfg.vectorEnabled || dims <= 0 {
		return nil
	}
	if m.vectorReady && m.vectorDims == dims {
		return nil
	}
	if m.vectorDims > 0 && m.vectorDims != dims {
		_, _ = m.db.Exec(`DROP TABLE IF EXISTS ` + vectorTableName)
	}
	_, err := m.db.Exec(fmt.Sprintf(
		`CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(
			id TEXT PRIMARY KEY,
			embedding FLOAT[%d]
		)`,
		vectorTableName,
		dims,
	))
	if err != nil {
		m.vectorReady = false
		m.lastError = err.Error()
		return err
	}
	m.vectorReady = true
	m.vectorDims = dims
	return nil
}

func (m *IndexManager) resetIndexLocked() error {
	_, err := m.db.Exec(`DELETE FROM files`)
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`DELETE FROM chunks`)
	if err != nil {
		return err
	}
	if m.ftsReady {
		_, _ = m.db.Exec(`DELETE FROM ` + ftsTableName)
	}
	if m.vectorDims > 0 {
		_, _ = m.db.Exec(`DROP TABLE IF EXISTS ` + vectorTableName)
	}
	m.vectorDims = 0
	m.vectorReady = false
	return nil
}

func (m *IndexManager) deletePathLocked(relPath string) error {
	if m.vectorReady {
		_, _ = m.db.Exec(
			`DELETE FROM `+vectorTableName+` WHERE id IN (SELECT id FROM chunks WHERE path = ?)`,
			relPath,
		)
	}
	if m.ftsReady {
		_, _ = m.db.Exec(
			`DELETE FROM `+ftsTableName+` WHERE path = ? AND model = ?`,
			relPath,
			m.cfg.model,
		)
	}
	if _, err := m.db.Exec(`DELETE FROM chunks WHERE path = ?`, relPath); err != nil {
		return err
	}
	if _, err := m.db.Exec(`DELETE FROM files WHERE path = ?`, relPath); err != nil {
		return err
	}
	return nil
}

func (m *IndexManager) indexFileLocked(ctx context.Context, entry memoryFileEntry) error {
	chunks := chunkMarkdown(entry.Content, m.cfg.chunkTokens, m.cfg.chunkOverlap)
	filtered := make([]chunkEntry, 0, len(chunks))
	for _, c := range chunks {
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		filtered = append(filtered, c)
	}

	embeddings, err := m.embedChunksWithCacheLocked(ctx, filtered)
	if err != nil {
		return err
	}
	dims := 0
	for _, v := range embeddings {
		if len(v) > 0 {
			dims = len(v)
			break
		}
	}
	vectorOK := false
	if dims > 0 {
		if err := m.ensureVectorTableLocked(dims); err != nil {
			return err
		}
		vectorOK = true
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	if vectorOK {
		_, _ = tx.Exec(
			`DELETE FROM `+vectorTableName+` WHERE id IN (SELECT id FROM chunks WHERE path = ?)`,
			entry.RelPath,
		)
	}
	if m.ftsReady {
		_, _ = tx.Exec(`DELETE FROM `+ftsTableName+` WHERE path = ? AND model = ?`, entry.RelPath, m.cfg.model)
	}
	if _, err := tx.Exec(`DELETE FROM chunks WHERE path = ?`, entry.RelPath); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	for i, c := range filtered {
		emb := embeddings[i]
		id := hashText(fmt.Sprintf("%s:%d:%d:%s:%s", entry.RelPath, c.StartLine, c.EndLine, c.Hash, m.cfg.model))
		embJSON, _ := json.Marshal(emb)
		if _, err := tx.Exec(
			`INSERT INTO chunks (id,path,start_line,end_line,hash,model,text,embedding,updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
			 	hash=excluded.hash,
				model=excluded.model,
				text=excluded.text,
				embedding=excluded.embedding,
				updated_at=excluded.updated_at`,
			id,
			entry.RelPath,
			c.StartLine,
			c.EndLine,
			c.Hash,
			m.cfg.model,
			c.Text,
			string(embJSON),
			now,
		); err != nil {
			return err
		}
		if vectorOK && len(emb) > 0 {
			_, _ = tx.Exec(`DELETE FROM `+vectorTableName+` WHERE id = ?`, id)
			if _, err := tx.Exec(
				`INSERT INTO `+vectorTableName+` (id, embedding) VALUES (?, ?)`,
				id,
				vectorToBlob(emb),
			); err != nil {
				m.vectorReady = false
				m.lastError = err.Error()
				return err
			}
		}
		if m.ftsReady {
			_, _ = tx.Exec(
				`INSERT INTO `+ftsTableName+` (text,id,path,model,start_line,end_line)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				c.Text,
				id,
				entry.RelPath,
				m.cfg.model,
				c.StartLine,
				c.EndLine,
			)
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO files(path,hash,mtime,size) VALUES (?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		 	hash=excluded.hash,
			mtime=excluded.mtime,
			size=excluded.size`,
		entry.RelPath,
		entry.Hash,
		entry.Modified,
		entry.Size,
	); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	rollback = false
	return nil
}

func (m *IndexManager) searchVectorLocked(queryVec []float64, limit int) ([]vectorResult, error) {
	if len(queryVec) == 0 || limit <= 0 {
		return []vectorResult{}, nil
	}
	if err := m.ensureVectorTableLocked(len(queryVec)); err != nil {
		return nil, err
	}
	rows, err := m.db.Query(
		`SELECT c.id, c.path, c.start_line, c.end_line, c.text, vec_distance_cosine(v.embedding, ?) AS dist
		   FROM `+vectorTableName+` v
		   JOIN chunks c ON c.id = v.id
		  WHERE c.model = ?
		  ORDER BY dist ASC
		  LIMIT ?`,
		vectorToBlob(queryVec),
		m.cfg.model,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]vectorResult, 0, limit)
	for rows.Next() {
		var id, p, text string
		var startLine, endLine int
		var dist float64
		if err := rows.Scan(&id, &p, &startLine, &endLine, &text, &dist); err != nil {
			return nil, err
		}
		score := 1 - dist
		out = append(out, vectorResult{
			ID: id,
			SearchResult: SearchResult{
				Path:      p,
				StartLine: startLine,
				EndLine:   endLine,
				Score:     score,
				Snippet:   truncateText(text, snippetMaxChars),
			},
			VectorScore: score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *IndexManager) searchKeywordLocked(query string, limit int) ([]keywordResult, error) {
	if !m.ftsReady || limit <= 0 {
		return []keywordResult{}, nil
	}
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return []keywordResult{}, nil
	}
	rows, err := m.db.Query(
		`SELECT id, path, start_line, end_line, text, bm25(`+ftsTableName+`) AS rank
		   FROM `+ftsTableName+`
		  WHERE `+ftsTableName+` MATCH ? AND model = ?
		  ORDER BY rank ASC
		  LIMIT ?`,
		ftsQuery,
		m.cfg.model,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]keywordResult, 0, limit)
	for rows.Next() {
		var id, p, text string
		var startLine, endLine int
		var rank float64
		if err := rows.Scan(&id, &p, &startLine, &endLine, &text, &rank); err != nil {
			return nil, err
		}
		textScore := bm25RankToScore(rank)
		out = append(out, keywordResult{
			ID: id,
			SearchResult: SearchResult{
				Path:      p,
				StartLine: startLine,
				EndLine:   endLine,
				Score:     textScore,
				Snippet:   truncateText(text, snippetMaxChars),
			},
			TextScore: textScore,
		})
	}
	return out, rows.Err()
}

func (m *IndexManager) embedChunksWithCacheLocked(ctx context.Context, chunks []chunkEntry) ([][]float64, error) {
	if len(chunks) == 0 {
		return [][]float64{}, nil
	}
	result := make([][]float64, len(chunks))
	missingIdx := make([]int, 0, len(chunks))

	cache := map[string][]float64{}
	if m.cfg.cacheEnabled {
		cache = m.loadEmbeddingCacheLocked(chunks)
	}
	for i, ch := range chunks {
		if vec, ok := cache[ch.Hash]; ok && len(vec) > 0 {
			result[i] = vec
			continue
		}
		missingIdx = append(missingIdx, i)
	}
	if len(missingIdx) == 0 {
		return result, nil
	}

	texts := make([]string, 0, len(missingIdx))
	for _, idx := range missingIdx {
		texts = append(texts, chunks[idx].Text)
	}
	emb, err := m.provider.EmbedBatch(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(emb) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch: got=%d want=%d", len(emb), len(texts))
	}
	toCache := make([]cacheRow, 0, len(missingIdx))
	for i, idx := range missingIdx {
		result[idx] = emb[i]
		toCache = append(toCache, cacheRow{
			Hash:      chunks[idx].Hash,
			Embedding: emb[i],
		})
	}
	if m.cfg.cacheEnabled {
		if err := m.upsertEmbeddingCacheLocked(toCache); err != nil {
			return nil, err
		}
	}
	return result, nil
}

type cacheRow struct {
	Hash      string
	Embedding []float64
}

func (m *IndexManager) loadEmbeddingCacheLocked(chunks []chunkEntry) map[string][]float64 {
	out := map[string][]float64{}
	if !m.cfg.cacheEnabled || len(chunks) == 0 {
		return out
	}
	uniq := make([]string, 0, len(chunks))
	seen := map[string]struct{}{}
	for _, ch := range chunks {
		if _, ok := seen[ch.Hash]; ok {
			continue
		}
		seen[ch.Hash] = struct{}{}
		uniq = append(uniq, ch.Hash)
	}
	baseArgs := []any{m.cfg.provider, m.cfg.model, m.provider.providerKey()}
	const batchSize = 400
	for start := 0; start < len(uniq); start += batchSize {
		end := min(start+batchSize, len(uniq))
		batch := uniq[start:end]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(baseArgs)+len(batch))
		args = append(args, baseArgs...)
		for _, h := range batch {
			args = append(args, h)
		}
		rows, err := m.db.Query(
			fmt.Sprintf(
				`SELECT hash, embedding FROM %s WHERE provider=? AND model=? AND provider_key=? AND hash IN (%s)`,
				cacheTableName,
				placeholders,
			),
			args...,
		)
		if err != nil {
			continue
		}
		for rows.Next() {
			var hash, embRaw string
			if err := rows.Scan(&hash, &embRaw); err == nil {
				out[hash] = parseEmbeddingJSON(embRaw)
			}
		}
		_ = rows.Close()
	}
	return out
}

func (m *IndexManager) upsertEmbeddingCacheLocked(rows []cacheRow) error {
	if !m.cfg.cacheEnabled || len(rows) == 0 {
		return nil
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	stmt, err := tx.Prepare(
		fmt.Sprintf(
			`INSERT INTO %s (provider,model,provider_key,hash,embedding,dims,updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(provider,model,provider_key,hash) DO UPDATE SET
			 	embedding=excluded.embedding,
				dims=excluded.dims,
				updated_at=excluded.updated_at`,
			cacheTableName,
		),
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UnixMilli()
	pkey := m.provider.providerKey()
	for _, row := range rows {
		embJSON, _ := json.Marshal(row.Embedding)
		if _, err := stmt.Exec(
			m.cfg.provider,
			m.cfg.model,
			pkey,
			row.Hash,
			string(embJSON),
			len(row.Embedding),
			now,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	rollback = false
	return nil
}

func (m *IndexManager) pruneEmbeddingCacheLocked() error {
	if !m.cfg.cacheEnabled || m.cfg.cacheMax <= 0 {
		return nil
	}
	count := queryCount(m.db, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, cacheTableName))
	if count <= m.cfg.cacheMax {
		return nil
	}
	excess := count - m.cfg.cacheMax
	_, err := m.db.Exec(
		fmt.Sprintf(
			`DELETE FROM %s WHERE rowid IN (
				SELECT rowid FROM %s ORDER BY updated_at ASC LIMIT ?
			)`,
			cacheTableName,
			cacheTableName,
		),
		excess,
	)
	return err
}

func (m *IndexManager) listMemoryFilesLocked() ([]memoryFileEntry, error) {
	paths, err := listMemoryPaths(m.workspaceDir)
	if err != nil {
		return nil, err
	}
	out := make([]memoryFileEntry, 0, len(paths))
	for _, abs := range paths {
		st, err := os.Stat(abs)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		content := string(b)
		rel, err := filepath.Rel(m.workspaceDir, abs)
		if err != nil {
			continue
		}
		out = append(out, memoryFileEntry{
			AbsPath:  abs,
			RelPath:  filepath.ToSlash(rel),
			Hash:     hashText(content),
			Size:     st.Size(),
			Modified: st.ModTime().UnixMilli(),
			Content:  content,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}

func resolveSearchConfig(cfg *config.Config, workspace string) (resolvedSearchConfig, error) {
	raw := cfg.Agents.Defaults.MemorySearch
	out := resolvedSearchConfig{
		enabled:            raw.EnabledValue(),
		provider:           normalizeMemorySearchProvider(raw.Provider),
		model:              strings.TrimSpace(raw.Model),
		baseURL:            strings.TrimSpace(raw.Remote.BaseURL),
		apiKey:             strings.TrimSpace(raw.Remote.APIKey),
		headers:            copyHeaders(raw.Remote.Headers),
		storePath:          strings.TrimSpace(raw.Store.Path),
		vectorEnabled:      raw.Store.Vector.EnabledValue(),
		chunkTokens:        raw.Chunking.Tokens,
		chunkOverlap:       raw.Chunking.Overlap,
		maxResults:         raw.Query.MaxResults,
		minScore:           config.DefaultMemorySearchMinScore,
		hybridVectorWeight: config.DefaultMemorySearchHybridVectorWeight,
		hybridTextWeight:   config.DefaultMemorySearchHybridTextWeight,
		candidateMul:       raw.Query.Hybrid.CandidateMultiplier,
		cacheEnabled:       raw.Cache.EnabledValue(),
		cacheMax:           raw.Cache.MaxEntries,
		syncOnSearch:       raw.Sync.OnSearchValue(),
	}
	if raw.Query.MinScore != nil {
		out.minScore = *raw.Query.MinScore
	}
	if raw.Query.Hybrid.VectorWeight != nil {
		out.hybridVectorWeight = *raw.Query.Hybrid.VectorWeight
	}
	if raw.Query.Hybrid.TextWeight != nil {
		out.hybridTextWeight = *raw.Query.Hybrid.TextWeight
	}
	if out.enabled {
		if out.model == "" {
			return out, errors.New("agents.defaults.memorySearch.model is required when enabled")
		}
		switch out.provider {
		case "openai":
		default:
			return out, fmt.Errorf("unsupported memorySearch.provider: %s", out.provider)
		}
	}
	if out.baseURL == "" {
		out.baseURL = config.DefaultOpenAIBaseURL
	}
	if out.apiKey == "" {
		if looksLikeOpenRouterBaseURL(out.baseURL) {
			out.apiKey = strings.TrimSpace(cfg.Env["OPENROUTER_API_KEY"])
		}
		if out.apiKey == "" {
			out.apiKey = strings.TrimSpace(cfg.Env["OPENAI_API_KEY"])
		}
		if out.apiKey == "" {
			out.apiKey = strings.TrimSpace(cfg.Env["OPENROUTER_API_KEY"])
		}
		if out.apiKey == "" {
			out.apiKey = strings.TrimSpace(cfg.LLM.APIKey)
		}
	}
	if out.storePath == "" {
		out.storePath = filepath.Join(workspace, ".memory", "index.sqlite")
	} else {
		pathValue := strings.ReplaceAll(out.storePath, "{workspace}", workspace)
		if !filepath.IsAbs(pathValue) {
			pathValue = filepath.Join(workspace, pathValue)
		}
		out.storePath = filepath.Clean(pathValue)
	}
	if out.chunkTokens <= 0 {
		out.chunkTokens = config.DefaultMemorySearchChunkTokens
	}
	if out.chunkOverlap < 0 {
		out.chunkOverlap = 0
	}
	if out.chunkOverlap >= out.chunkTokens {
		out.chunkOverlap = out.chunkTokens - 1
	}
	if out.maxResults <= 0 {
		out.maxResults = config.DefaultMemorySearchMaxResults
	}
	out.minScore = clampFloat(out.minScore, 0, 1)
	out.hybridVectorWeight = clampFloat(out.hybridVectorWeight, 0, 1)
	out.hybridTextWeight = clampFloat(out.hybridTextWeight, 0, 1)
	sum := out.hybridVectorWeight + out.hybridTextWeight
	if sum > 0 {
		out.hybridVectorWeight = out.hybridVectorWeight / sum
		out.hybridTextWeight = out.hybridTextWeight / sum
	}
	if out.candidateMul <= 0 {
		out.candidateMul = config.DefaultMemorySearchCandidateMultiplier
	}
	return out, nil
}

func looksLikeOpenRouterBaseURL(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(s, "openrouter.ai")
}

func normalizeMemorySearchProvider(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "openai"
	}
	return s
}

func (p *openAIEmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return [][]float64{}, nil
	}
	if strings.TrimSpace(p.model) == "" {
		return nil, errors.New("memory embedding model is empty")
	}
	endpoint := strings.TrimRight(p.baseURL, "/") + "/embeddings"
	reqBody := map[string]any{
		"model": p.model,
		"input": texts,
	}
	b, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range p.headers {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		buf := make([]byte, 0, 1024)
		tmp := make([]byte, 1024)
		for len(buf) < 4096 {
			n, _ := resp.Body.Read(tmp)
			if n <= 0 {
				break
			}
			buf = append(buf, tmp[:n]...)
		}
		return nil, fmt.Errorf("embeddings http %d: %s", resp.StatusCode, strings.TrimSpace(string(buf)))
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, errors.New("embeddings response has no data")
	}
	out := make([][]float64, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			continue
		}
		out[d.Index] = normalizeEmbedding(d.Embedding)
	}
	for i := range out {
		if len(out[i]) == 0 {
			return nil, fmt.Errorf("embedding index %d missing in response", i)
		}
	}
	return out, nil
}

func (p *openAIEmbeddingProvider) providerKey() string {
	headerPairs := make([]string, 0, len(p.headers))
	for k, v := range p.headers {
		if strings.EqualFold(strings.TrimSpace(k), "authorization") {
			continue
		}
		headerPairs = append(headerPairs, strings.TrimSpace(k)+"="+v)
	}
	sort.Strings(headerPairs)
	payload := fmt.Sprintf("%s|%s|%s|%s", p.provider, p.baseURL, p.model, strings.Join(headerPairs, "|"))
	return hashText(payload)
}

func normalizeEmbedding(vec []float64) []float64 {
	if len(vec) == 0 {
		return vec
	}
	var norm float64
	out := make([]float64, len(vec))
	for i, v := range vec {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			v = 0
		}
		out[i] = v
		norm += v * v
	}
	if norm <= 1e-10 {
		return out
	}
	scale := 1 / math.Sqrt(norm)
	for i := range out {
		out[i] *= scale
	}
	return out
}

func vectorToBlob(vec []float64) []byte {
	f32 := make([]float32, len(vec))
	for i, v := range vec {
		f32[i] = float32(v)
	}
	buf := make([]byte, len(f32)*4)
	for i, v := range f32 {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func parseEmbeddingJSON(raw string) []float64 {
	var out []float64
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []float64{}
	}
	return out
}

func chunkMarkdown(content string, tokens, overlap int) []chunkEntry {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}
	maxChars := maxInt(32, tokens*4)
	overlapChars := maxInt(0, overlap*4)
	type lineRec struct {
		line string
		no   int
	}
	cur := make([]lineRec, 0, 32)
	curChars := 0
	chunks := make([]chunkEntry, 0, 64)

	flush := func() {
		if len(cur) == 0 {
			return
		}
		textLines := make([]string, 0, len(cur))
		for _, rec := range cur {
			textLines = append(textLines, rec.line)
		}
		text := strings.Join(textLines, "\n")
		chunks = append(chunks, chunkEntry{
			StartLine: cur[0].no,
			EndLine:   cur[len(cur)-1].no,
			Text:      text,
			Hash:      hashText(text),
		})
	}

	carry := func() {
		if overlapChars <= 0 || len(cur) == 0 {
			cur = cur[:0]
			curChars = 0
			return
		}
		keep := make([]lineRec, 0, len(cur))
		acc := 0
		for i := len(cur) - 1; i >= 0; i-- {
			acc += len(cur[i].line) + 1
			keep = append(keep, cur[i])
			if acc >= overlapChars {
				break
			}
		}
		// reverse
		for i, j := 0, len(keep)-1; i < j; i, j = i+1, j-1 {
			keep[i], keep[j] = keep[j], keep[i]
		}
		cur = keep
		curChars = 0
		for _, k := range keep {
			curChars += len(k.line) + 1
		}
	}

	for i, line := range lines {
		lineNo := i + 1
		segments := []string{line}
		if line != "" && len(line) > maxChars {
			segments = segments[:0]
			for s := 0; s < len(line); s += maxChars {
				e := min(s+maxChars, len(line))
				segments = append(segments, line[s:e])
			}
		}
		for _, seg := range segments {
			size := len(seg) + 1
			if curChars+size > maxChars && len(cur) > 0 {
				flush()
				carry()
			}
			cur = append(cur, lineRec{line: seg, no: lineNo})
			curChars += size
		}
	}
	flush()
	return chunks
}

func listMemoryPaths(workspace string) ([]string, error) {
	var out []string
	addIfFile := func(abs string) {
		st, err := os.Lstat(abs)
		if err != nil || !st.Mode().IsRegular() || (st.Mode()&os.ModeSymlink) != 0 {
			return
		}
		if !strings.HasSuffix(strings.ToLower(abs), ".md") {
			return
		}
		out = append(out, abs)
	}
	addIfFile(filepath.Join(workspace, "MEMORY.md"))
	addIfFile(filepath.Join(workspace, "memory.md"))
	memDir := filepath.Join(workspace, "memory")
	_ = filepath.WalkDir(memDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".md") {
			addIfFile(path)
		}
		return nil
	})
	if len(out) <= 1 {
		return out, nil
	}
	seen := map[string]struct{}{}
	dedup := make([]string, 0, len(out))
	for _, p := range out {
		key := p
		if rp, err := filepath.EvalSymlinks(p); err == nil {
			key = rp
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dedup = append(dedup, p)
	}
	sort.Strings(dedup)
	return dedup, nil
}

func isMemoryPath(rel string) bool {
	normalized := filepath.ToSlash(strings.TrimSpace(rel))
	normalized = strings.TrimPrefix(normalized, "./")
	if normalized == "MEMORY.md" || normalized == "memory.md" {
		return true
	}
	return strings.HasPrefix(normalized, "memory/")
}

var tokenRe = regexp.MustCompile(`[A-Za-z0-9_]+`)

func buildFTSQuery(raw string) string {
	tokens := tokenRe.FindAllString(raw, -1)
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.TrimSpace(strings.ReplaceAll(t, `"`, ""))
		if t == "" {
			continue
		}
		parts = append(parts, `"`+t+`"`)
	}
	return strings.Join(parts, " AND ")
}

func bm25RankToScore(rank float64) float64 {
	if math.IsNaN(rank) || math.IsInf(rank, 0) {
		return 0
	}
	if rank < 0 {
		rank = 0
	}
	return 1 / (1 + rank)
}

func mergeHybrid(vector []vectorResult, keyword []keywordResult, vectorWeight, textWeight float64) []SearchResult {
	type merged struct {
		SearchResult
		vectorScore float64
		textScore   float64
	}
	byID := map[string]merged{}
	for _, r := range vector {
		byID[r.ID] = merged{
			SearchResult: SearchResult{
				Path:      r.Path,
				StartLine: r.StartLine,
				EndLine:   r.EndLine,
				Snippet:   r.Snippet,
			},
			vectorScore: r.VectorScore,
		}
	}
	for _, r := range keyword {
		cur, ok := byID[r.ID]
		if !ok {
			cur = merged{
				SearchResult: SearchResult{
					Path:      r.Path,
					StartLine: r.StartLine,
					EndLine:   r.EndLine,
					Snippet:   r.Snippet,
				},
			}
		} else if strings.TrimSpace(r.Snippet) != "" {
			cur.Snippet = r.Snippet
		}
		cur.textScore = r.TextScore
		byID[r.ID] = cur
	}
	out := make([]SearchResult, 0, len(byID))
	for _, row := range byID {
		score := vectorWeight*row.vectorScore + textWeight*row.textScore
		row.Score = score
		out = append(out, row.SearchResult)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func clampResults(results []SearchResult, maxResults int, minScore float64) []SearchResult {
	out := make([]SearchResult, 0, len(results))
	for _, r := range results {
		if r.Score < minScore {
			continue
		}
		out = append(out, r)
		if len(out) >= maxResults {
			break
		}
	}
	return out
}

func truncateText(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	if maxChars <= 0 {
		return ""
	}
	return s[:maxChars]
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func queryCount(db *sql.DB, q string) int {
	var c int
	_ = db.QueryRow(q).Scan(&c)
	return c
}

func copyHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
