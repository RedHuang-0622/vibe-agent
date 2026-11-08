package knowledge

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vibe-agent/internal/llm"
	knowledgev1 "vibe-agent/pkg/contracts/knowledge/v1"
)

const recordSchemaVersion = 1

type EmbeddingClient interface {
	Embed(context.Context, []string) (llm.EmbeddingBatch, error)
}

type RerankClient interface {
	Rerank(context.Context, llm.RerankRequest) (llm.RerankResponse, error)
}

type Config struct {
	LexicalCandidates int
	DenseCandidates   int
	RRFK              float64
	RerankCandidates  int
	ChunkRunes        int
	ChunkOverlap      int
}

func DefaultConfig() Config {
	return Config{
		LexicalCandidates: 50,
		DenseCandidates:   50,
		RRFK:              60,
		RerankCandidates:  20,
		ChunkRunes:        800,
		ChunkOverlap:      100,
	}
}

type ServiceError struct {
	Code      string
	Message   string
	Retryable bool
	Scope     string
	Provider  string
	RequestID string
	ResetAt   *time.Time
}

func (e *ServiceError) Error() string {
	return e.Code + ": " + e.Message
}

type chunk struct {
	ID          string    `json:"chunk_id"`
	RecordID    string    `json:"record_id"`
	Scope       string    `json:"scope"`
	Text        string    `json:"text"`
	Vector      []float32 `json:"vector,omitempty"`
	Fingerprint string    `json:"embedding_fingerprint,omitempty"`
}

type recordSnapshot struct {
	SchemaVersion  int               `json:"schema_version"`
	RecordID       string            `json:"record_id"`
	Version        int               `json:"version"`
	Scope          string            `json:"scope"`
	Text           string            `json:"text"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Status         string            `json:"status"`
	IdempotencyKey string            `json:"idempotency_key"`
	UpdatedAt      time.Time         `json:"updated_at"`
	Chunks         []chunk           `json:"chunks"`
}

type Service struct {
	mu         sync.RWMutex
	path       string
	config     Config
	embedder   EmbeddingClient
	reranker   RerankClient
	records    map[string]recordSnapshot
	idempotent map[string]string
	lexical    *BM25Index
}

func Open(path string, embedder EmbeddingClient, reranker RerankClient, config Config) (*Service, error) {
	if config.LexicalCandidates <= 0 {
		config = DefaultConfig()
	}
	service := &Service{
		path:       path,
		config:     config,
		embedder:   embedder,
		reranker:   reranker,
		records:    make(map[string]recordSnapshot),
		idempotent: make(map[string]string),
		lexical:    NewBM25Index(DefaultTokenizer{}, 1.2, 0.75),
	}
	if err := service.load(); err != nil {
		return nil, err
	}
	service.rebuildLexical()
	return service, nil
}

func (s *Service) load() error {
	file, err := os.Open(filepath.Join(s.path, "records.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		var snapshot recordSnapshot
		if err := json.Unmarshal(scanner.Bytes(), &snapshot); err != nil {
			return fmt.Errorf("decode memory record: %w", err)
		}
		if snapshot.SchemaVersion != recordSchemaVersion {
			return fmt.Errorf("unsupported memory schema version %d", snapshot.SchemaVersion)
		}
		current, exists := s.records[snapshot.RecordID]
		if !exists || snapshot.Version >= current.Version {
			s.records[snapshot.RecordID] = snapshot
			s.idempotent[snapshot.IdempotencyKey] = snapshot.RecordID
		}
	}
	return scanner.Err()
}

func (s *Service) Write(ctx context.Context, request knowledgev1.WriteRequest) (knowledgev1.RecordRef, error) {
	if err := validateWrite(request); err != nil {
		return knowledgev1.RecordRef{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if recordID, ok := s.idempotent[request.IdempotencyKey]; ok {
		record := s.records[recordID]
		return recordRef(record), nil
	}
	recordID := request.ID
	if recordID == "" {
		recordID = randomID("mem")
	}
	if _, exists := s.records[recordID]; exists {
		return knowledgev1.RecordRef{}, &ServiceError{Code: "conflict", Message: "memory record already exists"}
	}
	record, err := s.prepareRecord(ctx, recordID, 1, request)
	if err != nil {
		return knowledgev1.RecordRef{}, err
	}
	if err := s.publishLocked(record); err != nil {
		return knowledgev1.RecordRef{}, err
	}
	return recordRef(record), nil
}

func (s *Service) Update(ctx context.Context, request knowledgev1.UpdateRequest) (knowledgev1.RecordRef, error) {
	if request.RecordID == "" || request.IdempotencyKey == "" {
		return knowledgev1.RecordRef{}, &ServiceError{Code: "invalid_request", Message: "record_id and idempotency_key are required"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if recordID, ok := s.idempotent[request.IdempotencyKey]; ok {
		return recordRef(s.records[recordID]), nil
	}
	current, ok := s.records[request.RecordID]
	if !ok {
		return knowledgev1.RecordRef{}, &ServiceError{Code: "not_found", Message: "memory record not found"}
	}
	if strings.TrimSpace(request.Text) == "" {
		return knowledgev1.RecordRef{}, &ServiceError{Code: "invalid_request", Message: "text is required"}
	}
	if request.ExpectedVersion > 0 && current.Version != request.ExpectedVersion {
		return knowledgev1.RecordRef{}, &ServiceError{Code: "conflict", Message: "memory record version conflict"}
	}
	metadata := cloneMetadata(current.Metadata)
	for key, value := range request.PatchMetadata {
		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata[key] = value
	}
	record, err := s.prepareRecord(ctx, current.RecordID, current.Version+1, knowledgev1.WriteRequest{
		Scope: current.Scope, Text: request.Text, Metadata: metadata,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return knowledgev1.RecordRef{}, err
	}
	if err := s.publishLocked(record); err != nil {
		return knowledgev1.RecordRef{}, err
	}
	return recordRef(record), nil
}

func (s *Service) prepareRecord(ctx context.Context, recordID string, version int, request knowledgev1.WriteRequest) (recordSnapshot, error) {
	if s.embedder == nil {
		return recordSnapshot{}, &ServiceError{Code: "dependency_unavailable", Message: "embedding client is unavailable", Retryable: true}
	}
	texts := chunkText(request.Text, s.config.ChunkRunes, s.config.ChunkOverlap)
	batch, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return recordSnapshot{}, mapLLMError(err)
	}
	if len(batch.Vectors) != len(texts) {
		return recordSnapshot{}, &ServiceError{Code: "upstream_schema_error", Message: "embedding count does not match chunks"}
	}
	chunks := make([]chunk, len(texts))
	for index, text := range texts {
		chunks[index] = chunk{
			ID: fmt.Sprintf("%s_%d_%d", recordID, version, index), RecordID: recordID,
			Scope: request.Scope, Text: text, Vector: batch.Vectors[index],
			Fingerprint: batch.Fingerprint,
		}
	}
	return recordSnapshot{
		SchemaVersion: recordSchemaVersion, RecordID: recordID, Version: version,
		Scope: request.Scope, Text: request.Text, Metadata: cloneMetadata(request.Metadata),
		Status: "ready", IdempotencyKey: request.IdempotencyKey, UpdatedAt: time.Now().UTC(),
		Chunks: chunks,
	}, nil
}

func (s *Service) publishLocked(record recordSnapshot) error {
	if err := os.MkdirAll(s.path, 0o700); err != nil {
		return err
	}
	records := make(map[string]recordSnapshot, len(s.records)+1)
	for id, value := range s.records {
		records[id] = value
	}
	records[record.RecordID] = record
	if err := writeVectorsAtomic(s.path, records); err != nil {
		return &ServiceError{Code: "dependency_unavailable", Message: "vector index write failed", Retryable: true}
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(s.path, "records.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(line, '\n'))
	if syncErr := file.Sync(); writeErr == nil {
		writeErr = syncErr
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return writeErr
	}
	s.records[record.RecordID] = record
	s.idempotent[record.IdempotencyKey] = record.RecordID
	s.rebuildLexical()
	return nil
}

func (s *Service) rebuildLexical() {
	documents := make([]LexicalDocument, 0)
	for _, record := range s.records {
		if record.Status != "ready" {
			continue
		}
		for _, chunk := range record.Chunks {
			documents = append(documents, LexicalDocument{
				RecordID: record.RecordID, ChunkID: chunk.ID,
				Scope: record.Scope, Text: chunk.Text,
			})
		}
	}
	s.lexical.Replace(context.Background(), documents)
}

func recordRef(record recordSnapshot) knowledgev1.RecordRef {
	return knowledgev1.RecordRef{
		RecordID: record.RecordID, Version: record.Version,
		Status: record.Status, UpdatedAt: record.UpdatedAt,
	}
}

func validateWrite(request knowledgev1.WriteRequest) error {
	if strings.TrimSpace(request.Text) == "" || request.IdempotencyKey == "" {
		return &ServiceError{Code: "invalid_request", Message: "text and idempotency_key are required"}
	}
	if err := validateScope(request.Scope); err != nil {
		return err
	}
	return nil
}

func validateScope(scope string) error {
	switch scope {
	case "workspace", "user", "global":
		return nil
	default:
		return &ServiceError{Code: "invalid_request", Message: "unsupported memory scope"}
	}
}

func cloneMetadata(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func mapLLMError(err error) error {
	var serviceErr *ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr
	}
	var llmErr *llm.Error
	if errors.As(err, &llmErr) {
		return &ServiceError{
			Code: llmErr.Code, Message: llmErr.Message, Retryable: llmErr.Retryable,
			Scope: llmErr.Scope, Provider: llmErr.Provider,
			RequestID: llmErr.RequestID, ResetAt: llmErr.ResetAt,
		}
	}
	return &ServiceError{Code: "dependency_unavailable", Message: "embedding service failed", Retryable: true}
}

func randomID(prefix string) string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(value)
}

func chunkText(text string, size, overlap int) []string {
	runes := []rune(text)
	if size <= 0 || len(runes) <= size {
		return []string{text}
	}
	if overlap < 0 || overlap >= size {
		overlap = size / 4
	}
	var chunks []string
	for start := 0; start < len(runes); start += size - overlap {
		end := min(start+size, len(runes))
		chunks = append(chunks, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return chunks
}

type persistedVectors struct {
	Records []recordSnapshot `json:"records"`
}

func writeVectorsAtomic(path string, records map[string]recordSnapshot) error {
	values := make([]recordSnapshot, 0, len(records))
	for _, record := range records {
		values = append(values, record)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].RecordID < values[j].RecordID })
	encoded, err := json.Marshal(persistedVectors{Records: values})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(path, ".vectors-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(encoded); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, filepath.Join(path, "vectors.json"))
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / math.Sqrt(normA*normB)
}
