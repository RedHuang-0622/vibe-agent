package adapters

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vibe-agent/internal/accounts/domain"
)

type diskBinding struct {
	Capability      domain.Capability `json:"capability"`
	Provider        string            `json:"provider"`
	Protocol        string            `json:"protocol"`
	BaseURL         string            `json:"base_url"`
	Path            string            `json:"path"`
	Model           string            `json:"model"`
	APIKeyEnv       string            `json:"api_key_env,omitempty"`
	CredentialRef   string            `json:"credential_ref"`
	Thinking        *diskThinking     `json:"thinking,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	Dimensions      int               `json:"dimensions,omitempty"`
	BatchSize       int               `json:"batch_size,omitempty"`
	Normalize       bool              `json:"normalize,omitempty"`
	TopN            int               `json:"top_n,omitempty"`
	MaxCandidates   int               `json:"max_candidates,omitempty"`
	TimeoutMS       int64             `json:"timeout_ms"`
	Timeout         string            `json:"timeout,omitempty"`
	Capabilities    []string          `json:"capabilities,omitempty"`
	Retry           json.RawMessage   `json:"retry,omitempty"`
	Limits          json.RawMessage   `json:"limits,omitempty"`
	Probe           json.RawMessage   `json:"probe,omitempty"`
}

type diskThinking struct {
	Type string `json:"type"`
}

type diskAccount struct {
	SchemaVersion int                               `json:"schema_version"`
	AccountID     string                            `json:"account_id"`
	Revision      uint64                            `json:"revision"`
	Enabled       bool                              `json:"enabled"`
	Archived      bool                              `json:"archived,omitempty"`
	DisplayName   string                            `json:"display_name"`
	Priority      int                               `json:"priority"`
	DefaultFor    []domain.Capability               `json:"default_for,omitempty"`
	Services      map[domain.Capability]diskBinding `json:"services"`
	Metadata      json.RawMessage                   `json:"metadata,omitempty"`
}

type JSONLRepository struct {
	mu       sync.RWMutex
	path     string
	accounts map[string]domain.Account
}

func OpenJSONLRepository(path string) (*JSONLRepository, error) {
	repo := &JSONLRepository{path: path, accounts: make(map[string]domain.Account)}
	if err := repo.reload(); err != nil {
		return nil, err
	}
	return repo, nil
}

func (r *JSONLRepository) reload() error {
	file, err := os.Open(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open account store: %w", err)
	}
	defer file.Close()
	loaded := make(map[string]domain.Account)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			return fmt.Errorf("account store line %d is empty", line)
		}
		var record diskAccount
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return fmt.Errorf("account store line %d: %w", line, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return fmt.Errorf("account store line %d: trailing JSON value", line)
		}
		if record.SchemaVersion != 1 {
			return fmt.Errorf("account store line %d: unsupported schema version %d", line, record.SchemaVersion)
		}
		account := fromDisk(record)
		if _, duplicate := loaded[account.ID]; duplicate {
			return fmt.Errorf("account store line %d: duplicate account %q", line, account.ID)
		}
		if err := account.Validate(); err != nil {
			return fmt.Errorf("account %q: %w", account.ID, err)
		}
		loaded[account.ID] = account
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read account store: %w", err)
	}
	r.mu.Lock()
	r.accounts = loaded
	r.mu.Unlock()
	return nil
}

func (r *JSONLRepository) List(context.Context) ([]domain.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	accounts := make([]domain.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		accounts = append(accounts, cloneAccount(account))
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	return accounts, nil
}

func (r *JSONLRepository) Get(_ context.Context, accountID string) (domain.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	account, ok := r.accounts[accountID]
	if !ok {
		return domain.Account{}, os.ErrNotExist
	}
	return cloneAccount(account), nil
}

func (r *JSONLRepository) Save(_ context.Context, account domain.Account, expectedRevision uint64) error {
	if err := account.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.accounts[account.ID]
	if exists && current.Revision != expectedRevision {
		return fmt.Errorf("revision conflict: expected %d, current %d", expectedRevision, current.Revision)
	}
	if !exists && expectedRevision != 0 {
		return fmt.Errorf("revision conflict: account does not exist")
	}
	next := make(map[string]domain.Account, len(r.accounts)+1)
	for id, value := range r.accounts {
		next[id] = value
	}
	next[account.ID] = cloneAccount(account)
	if err := r.write(next); err != nil {
		return err
	}
	r.accounts = next
	return nil
}

func (r *JSONLRepository) write(accounts map[string]domain.Account) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("create account directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(r.path), ".accounts-*.tmp")
	if err != nil {
		return fmt.Errorf("create account temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	ids := make([]string, 0, len(accounts))
	for id := range accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	encoder := json.NewEncoder(temp)
	for _, id := range ids {
		if err := encoder.Encode(toDisk(accounts[id])); err != nil {
			_ = temp.Close()
			return fmt.Errorf("encode account: %w", err)
		}
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync account file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, r.path); err != nil {
		return fmt.Errorf("replace account file: %w", err)
	}
	return nil
}

func fromDisk(record diskAccount) domain.Account {
	account := domain.Account{
		ID:          record.AccountID,
		Revision:    record.Revision,
		Enabled:     record.Enabled,
		Archived:    record.Archived,
		DisplayName: record.DisplayName,
		Priority:    record.Priority,
		DefaultFor:  make(map[domain.Capability]bool),
		Services:    make(map[domain.Capability]domain.Binding),
	}
	for _, capability := range record.DefaultFor {
		account.DefaultFor[capability] = true
	}
	for key, value := range record.Services {
		capability := value.Capability
		if capability == "" {
			capability = key
		}
		credentialRef := value.CredentialRef
		if credentialRef == "" && value.APIKeyEnv != "" {
			credentialRef = "env:" + value.APIKeyEnv
		}
		thinkingType := ""
		if value.Thinking != nil {
			thinkingType = value.Thinking.Type
		}
		account.Services[key] = domain.Binding{
			Capability:      capability,
			Provider:        value.Provider,
			Protocol:        value.Protocol,
			BaseURL:         value.BaseURL,
			Path:            value.Path,
			Model:           value.Model,
			CredentialRef:   credentialRef,
			ThinkingType:    thinkingType,
			ReasoningEffort: value.ReasoningEffort,
			Dimensions:      value.Dimensions,
			BatchSize:       value.BatchSize,
			Normalize:       value.Normalize,
			TopN:            value.TopN,
			MaxCandidates:   value.MaxCandidates,
			TimeoutMS:       bindingTimeoutMS(value.TimeoutMS, value.Timeout),
		}
	}
	account.Services = domain.NormalizeServices(account.Services)
	return account
}

func toDisk(account domain.Account) diskAccount {
	record := diskAccount{
		SchemaVersion: 1,
		AccountID:     account.ID,
		Revision:      account.Revision,
		Enabled:       account.Enabled,
		Archived:      account.Archived,
		DisplayName:   account.DisplayName,
		Priority:      account.Priority,
		Services:      make(map[domain.Capability]diskBinding),
	}
	for capability, selected := range account.DefaultFor {
		if selected {
			record.DefaultFor = append(record.DefaultFor, capability)
		}
	}
	sort.Slice(record.DefaultFor, func(i, j int) bool { return record.DefaultFor[i] < record.DefaultFor[j] })
	for capability, binding := range account.Services {
		thinking := &diskThinking{Type: binding.ThinkingType}
		if binding.ThinkingType == "" {
			thinking = nil
		}
		record.Services[capability] = diskBinding{
			Capability:      binding.Capability,
			Provider:        binding.Provider,
			Protocol:        binding.Protocol,
			BaseURL:         binding.BaseURL,
			Path:            binding.Path,
			Model:           binding.Model,
			CredentialRef:   binding.CredentialRef,
			Thinking:        thinking,
			ReasoningEffort: binding.ReasoningEffort,
			Dimensions:      binding.Dimensions,
			BatchSize:       binding.BatchSize,
			Normalize:       binding.Normalize,
			TopN:            binding.TopN,
			MaxCandidates:   binding.MaxCandidates,
			TimeoutMS:       binding.TimeoutMS,
		}
	}
	return record
}

func cloneAccount(account domain.Account) domain.Account {
	cloned := account
	cloned.DefaultFor = make(map[domain.Capability]bool, len(account.DefaultFor))
	for key, value := range account.DefaultFor {
		cloned.DefaultFor[key] = value
	}
	cloned.Services = make(map[domain.Capability]domain.Binding, len(account.Services))
	for key, value := range account.Services {
		cloned.Services[key] = value
	}
	return cloned
}

func bindingTimeoutMS(milliseconds int64, duration string) int64 {
	if milliseconds > 0 || strings.TrimSpace(duration) == "" {
		return milliseconds
	}
	parsed, err := time.ParseDuration(duration)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed.Milliseconds()
}
