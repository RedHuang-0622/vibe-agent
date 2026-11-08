package knowledge

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

const DefaultLexicalIndexVersion = "cjk-uni-bigram-v1"

type Tokenizer interface {
	Tokenize(text string) []string
	Version() string
}

type DefaultTokenizer struct{}

func (DefaultTokenizer) Version() string {
	return DefaultLexicalIndexVersion
}

func (DefaultTokenizer) Tokenize(text string) []string {
	var tokens []string
	runes := []rune(strings.ToLower(text))
	for i := 0; i < len(runes); {
		r := runes[i]
		if isLatinToken(r) {
			start := i
			for i < len(runes) && isLatinToken(runes[i]) {
				i++
			}
			tokens = append(tokens, string(runes[start:i]))
			continue
		}
		if isCJK(r) {
			start := i
			for i < len(runes) && isCJK(runes[i]) {
				i++
			}
			segment := runes[start:i]
			for offset, current := range segment {
				tokens = append(tokens, string(current))
				if offset+1 < len(segment) {
					tokens = append(tokens, string(segment[offset:offset+2]))
				}
			}
			continue
		}
		i++
	}
	return tokens
}

func isLatinToken(r rune) bool {
	return unicode.IsLetter(r) && r < 0x2e80 || unicode.IsDigit(r)
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

type LexicalDocument struct {
	RecordID string
	ChunkID  string
	Scope    string
	Text     string
}

type LexicalHit struct {
	RecordID string
	ChunkID  string
	Scope    string
	Text     string
	Score    float64
}

type indexedDocument struct {
	RecordID string
	ChunkID  string
	Scope    string
	Text     string
	Length   int
}

type lexicalSnapshot struct {
	documents map[string]indexedDocument
	postings  map[string]map[string]int
	avgLength float64
	version   string
}

type BM25Index struct {
	mu        sync.RWMutex
	tokenizer Tokenizer
	k1        float64
	b         float64
	snapshot  lexicalSnapshot
}

func NewBM25Index(tokenizer Tokenizer, k1, b float64) *BM25Index {
	if tokenizer == nil {
		tokenizer = DefaultTokenizer{}
	}
	if k1 <= 0 {
		k1 = 1.2
	}
	if b < 0 || b > 1 {
		b = 0.75
	}
	return &BM25Index{
		tokenizer: tokenizer,
		k1:        k1,
		b:         b,
		snapshot: lexicalSnapshot{
			documents: make(map[string]indexedDocument),
			postings:  make(map[string]map[string]int),
			version:   tokenizer.Version(),
		},
	}
}

func (i *BM25Index) Replace(_ context.Context, documents []LexicalDocument) {
	i.mu.Lock()
	i.snapshot = i.buildSnapshot(documents)
	i.mu.Unlock()
}

func (i *BM25Index) buildSnapshot(documents []LexicalDocument) lexicalSnapshot {
	next := lexicalSnapshot{
		documents: make(map[string]indexedDocument, len(documents)),
		postings:  make(map[string]map[string]int),
		version:   i.tokenizer.Version(),
	}
	var totalLength int
	for _, document := range documents {
		tokens := i.tokenizer.Tokenize(document.Text)
		next.documents[document.ChunkID] = indexedDocument{
			RecordID: document.RecordID,
			ChunkID:  document.ChunkID,
			Scope:    document.Scope,
			Text:     document.Text,
			Length:   len(tokens),
		}
		totalLength += len(tokens)
		for _, token := range tokens {
			if next.postings[token] == nil {
				next.postings[token] = make(map[string]int)
			}
			next.postings[token][document.ChunkID]++
		}
	}
	if len(documents) > 0 {
		next.avgLength = float64(totalLength) / float64(len(documents))
	}
	return next
}

func (i *BM25Index) Upsert(_ context.Context, documents []LexicalDocument) {
	i.mu.Lock()
	current := make([]LexicalDocument, 0, len(i.snapshot.documents)+len(documents))
	for _, document := range i.snapshot.documents {
		current = append(current, LexicalDocument{
			RecordID: document.RecordID,
			ChunkID:  document.ChunkID,
			Scope:    document.Scope,
			Text:     document.Text,
		})
	}
	incoming := make(map[string]bool, len(documents))
	for _, document := range documents {
		incoming[document.ChunkID] = true
	}
	filtered := current[:0]
	for _, document := range current {
		if !incoming[document.ChunkID] {
			filtered = append(filtered, document)
		}
	}
	i.snapshot = i.buildSnapshot(append(filtered, documents...))
	i.mu.Unlock()
}

func (i *BM25Index) Delete(_ context.Context, chunkIDs []string) {
	remove := make(map[string]bool, len(chunkIDs))
	for _, id := range chunkIDs {
		remove[id] = true
	}
	i.mu.Lock()
	kept := make([]LexicalDocument, 0, len(i.snapshot.documents))
	for _, document := range i.snapshot.documents {
		if remove[document.ChunkID] {
			continue
		}
		kept = append(kept, LexicalDocument{
			RecordID: document.RecordID, ChunkID: document.ChunkID,
			Scope: document.Scope, Text: document.Text,
		})
	}
	i.snapshot = i.buildSnapshot(kept)
	i.mu.Unlock()
}

func (i *BM25Index) Search(_ context.Context, query, scope string, limit int) []LexicalHit {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if limit <= 0 {
		limit = 10
	}
	terms := unique(i.tokenizer.Tokenize(query))
	totalDocuments := float64(len(i.snapshot.documents))
	if totalDocuments == 0 || len(terms) == 0 {
		return []LexicalHit{}
	}
	scores := make(map[string]float64)
	for _, term := range terms {
		postings, ok := i.snapshot.postings[term]
		if !ok {
			continue
		}
		idf := math.Log(1 + (totalDocuments-float64(len(postings))+0.5)/(float64(len(postings))+0.5))
		for chunkID, frequency := range postings {
			document := i.snapshot.documents[chunkID]
			if scope != "" && document.Scope != scope {
				continue
			}
			tf := float64(frequency)
			lengthRatio := 0.0
			if i.snapshot.avgLength > 0 {
				lengthRatio = float64(document.Length) / i.snapshot.avgLength
			}
			scores[chunkID] += idf * (tf * (i.k1 + 1)) / (tf + i.k1*(1-i.b+i.b*lengthRatio))
		}
	}
	hits := make([]LexicalHit, 0, len(scores))
	for chunkID, score := range scores {
		document := i.snapshot.documents[chunkID]
		hits = append(hits, LexicalHit{
			RecordID: document.RecordID,
			ChunkID:  document.ChunkID,
			Scope:    document.Scope,
			Text:     document.Text,
			Score:    score,
		})
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].Score == hits[b].Score {
			return hits[a].ChunkID < hits[b].ChunkID
		}
		return hits[a].Score > hits[b].Score
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func (i *BM25Index) Version() string {
	return i.tokenizer.Version()
}

func unique(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
