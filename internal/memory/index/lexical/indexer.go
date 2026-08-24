package lexical

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/model"
)

type SearchResult struct {
	MemoryID       string  `json:"memory_id"`
	Score          float64 `json:"score"`
	ExactTitleSeed bool    `json:"exact_title_seed,omitempty"`
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
	mu       sync.RWMutex
	docs     map[string]IndexedDocument
	postings map[string]*postingList
}

// postingList avoids allocating a Go map for the common case where a token is
// unique (paths, symbols, error digests). It promotes to a set only when a
// second document uses the token.
type postingList struct {
	one  string
	many map[string]struct{}
}

func (p *postingList) add(id string) {
	if p.many != nil {
		p.many[id] = struct{}{}
		return
	}
	if p.one == "" || p.one == id {
		p.one = id
		return
	}
	p.many = map[string]struct{}{p.one: {}, id: {}}
	p.one = ""
}

func (p *postingList) remove(id string) {
	if p.many == nil {
		if p.one == id {
			p.one = ""
		}
		return
	}
	delete(p.many, id)
	if len(p.many) == 1 {
		for remaining := range p.many {
			p.one = remaining
		}
		p.many = nil
	}
}

func (p *postingList) len() int {
	if p == nil {
		return 0
	}
	if p.many != nil {
		return len(p.many)
	}
	if p.one != "" {
		return 1
	}
	return 0
}

func (p *postingList) ids() []string {
	if p == nil {
		return nil
	}
	if p.many == nil {
		if p.one == "" {
			return nil
		}
		return []string{p.one}
	}
	ids := make([]string, 0, len(p.many))
	for id := range p.many {
		ids = append(ids, id)
	}
	return ids
}

func NewLexicalIndex() *LexicalIndex {
	return &LexicalIndex{
		docs: make(map[string]IndexedDocument), postings: make(map[string]*postingList),
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
	idx.removeLocked(rec.ID)

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
	for token := range tokenMap {
		if idx.postings[token] == nil {
			idx.postings[token] = &postingList{}
		}
		idx.postings[token].add(rec.ID)
	}

	return nil
}

// DeleteRecord permanently removes a record from the lexical index.
func (idx *LexicalIndex) DeleteRecord(ctx context.Context, memoryID string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.removeLocked(memoryID)
	return nil
}

// RemoveRecord deletes a memory record from the lexical index.
func (idx *LexicalIndex) RemoveRecord(ctx context.Context, memoryID string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.removeLocked(memoryID)
	return nil
}

// Rebuild resets the index and repopulates from canonical records.
func (idx *LexicalIndex) Rebuild(ctx context.Context, records []model.MemoryRecordV2) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.docs = make(map[string]IndexedDocument)
	idx.postings = make(map[string]*postingList)
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
		for token := range tokenMap {
			if idx.postings[token] == nil {
				idx.postings[token] = &postingList{}
			}
			idx.postings[token].add(rec.ID)
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

	uniqueQueryTokens := make(map[string]struct{}, len(qTokens))
	var candidates *postingList
	for _, token := range qTokens {
		if _, duplicate := uniqueQueryTokens[token]; duplicate {
			continue
		}
		uniqueQueryTokens[token] = struct{}{}
		posting := idx.postings[token]
		if posting.len() == 0 {
			continue
		}
		if candidates == nil || posting.len() < candidates.len() {
			candidates = posting
		}
	}
	if candidates.len() == 0 {
		return nil, nil
	}

	var scored []SearchResult
	for _, memoryID := range candidates.ids() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if authorizedIDs != nil {
			if _, allowed := authorizedIDs[memoryID]; !allowed {
				continue
			}
		}
		doc, exists := idx.docs[memoryID]
		if !exists {
			continue
		}
		if doc.ProjectID != projectID {
			continue
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
				MemoryID:       doc.MemoryID,
				Score:          score,
				ExactTitleSeed: strings.Contains(strings.ToLower(doc.Title), lowerQuery),
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

func (idx *LexicalIndex) removeLocked(memoryID string) {
	doc, exists := idx.docs[memoryID]
	if !exists {
		return
	}
	for token := range doc.Tokens {
		posting := idx.postings[token]
		posting.remove(memoryID)
		if posting.len() == 0 {
			delete(idx.postings, token)
		}
	}
	delete(idx.docs, memoryID)
}
