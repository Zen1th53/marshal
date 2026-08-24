package lexical

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/model"
)

type SearchResult struct {
	MemoryID string  `json:"memory_id"`
	Score    float64 `json:"score"`
}

type IndexedDocument struct {
	MemoryID  string
	ProjectID string
	Title     string
	Body      string
	Scope     string
	ScopeID   string
	Tokens    map[string]int
}

type LexicalIndex struct {
	mu   sync.RWMutex
	docs map[string]IndexedDocument
}

func NewLexicalIndex() *LexicalIndex {
	return &LexicalIndex{
		docs: make(map[string]IndexedDocument),
	}
}

func tokenize(text string) []string {
	clean := strings.ToLower(text)
	// Replace common delimiters with spaces
	replacer := strings.NewReplacer("/", " ", ".", " ", "_", " ", "-", " ", ":", " ", "(", " ", ")", " ", "[", " ", "]", " ", "\"", " ", "'", " ")
	clean = replacer.Replace(clean)
	return strings.Fields(clean)
}

// IndexRecord adds or updates a memory record in the lexical index.
func (idx *LexicalIndex) IndexRecord(ctx context.Context, rec model.MemoryRecordV2) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Omit tombstoned records
	if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
		delete(idx.docs, rec.ID)
		return nil
	}

	tokenMap := make(map[string]int)
	fullText := rec.Title + " " + rec.Body
	for _, tok := range tokenize(fullText) {
		tokenMap[tok]++
	}

	idx.docs[rec.ID] = IndexedDocument{
		MemoryID:  rec.ID,
		ProjectID: rec.ProjectID,
		Title:     rec.Title,
		Body:      rec.Body,
		Scope:     rec.Scope,
		ScopeID:   rec.ScopeID,
		Tokens:    tokenMap,
	}

	return nil
}

// DeleteRecord permanently removes a record from the lexical index.
func (idx *LexicalIndex) DeleteRecord(ctx context.Context, memoryID string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.docs, memoryID)
	return nil
}

// RemoveRecord deletes a memory record from the lexical index.
func (idx *LexicalIndex) RemoveRecord(ctx context.Context, memoryID string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	delete(idx.docs, memoryID)
	return nil
}

// Rebuild resets the index and repopulates from canonical records.
func (idx *LexicalIndex) Rebuild(ctx context.Context, records []model.MemoryRecordV2) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.docs = make(map[string]IndexedDocument)
	for _, rec := range records {
		if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
			continue
		}
		tokenMap := make(map[string]int)
		fullText := rec.Title + " " + rec.Body
		for _, tok := range tokenize(fullText) {
			tokenMap[tok]++
		}
		idx.docs[rec.ID] = IndexedDocument{
			MemoryID:  rec.ID,
			ProjectID: rec.ProjectID,
			Title:     rec.Title,
			Body:      rec.Body,
			Scope:     rec.Scope,
			ScopeID:   rec.ScopeID,
			Tokens:    tokenMap,
		}
	}
	return nil
}

// Search performs term-matching and exact-phrase boosted search over the lexical index.
func (idx *LexicalIndex) Search(ctx context.Context, projectID, query string, limit int) ([]SearchResult, error) {
	return idx.SearchAuthorized(ctx, projectID, query, nil, limit)
}

// SearchAuthorized applies the caller's canonical authorization set before
// examining document content or calculating a score. A nil set is retained
// only for index unit tests and trusted maintenance callers.
func (idx *LexicalIndex) SearchAuthorized(ctx context.Context, projectID, query string, authorizedIDs map[string]struct{}, limit int) ([]SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}

	qTokens := tokenize(q)
	lowerQuery := strings.ToLower(q)

	var scored []SearchResult

	for _, doc := range idx.docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if doc.ProjectID != projectID {
			continue
		}
		if authorizedIDs != nil {
			if _, allowed := authorizedIDs[doc.MemoryID]; !allowed {
				continue
			}
		}

		var score float64
		fullText := strings.ToLower(doc.Title + " " + doc.Body)

		// 1. Exact string / path / symbol boost
		if strings.Contains(fullText, lowerQuery) {
			score += 10.0
		}

		// 2. Token matches
		matchCount := 0
		for _, tok := range qTokens {
			if count, ok := doc.Tokens[tok]; ok {
				score += float64(count) * 1.5
				matchCount++
			}
		}

		// 3. Exact title match boost
		if strings.Contains(strings.ToLower(doc.Title), lowerQuery) {
			score += 5.0
		}

		if score > 0 {
			scored = append(scored, SearchResult{
				MemoryID: doc.MemoryID,
				Score:    score,
			})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}

	return scored, nil
}
