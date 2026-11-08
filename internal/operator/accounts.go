package operator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	accountapp "vibe-agent/internal/accounts/app"
	"vibe-agent/internal/accounts/domain"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

type accountFileRecord struct {
	SchemaVersion int                                     `json:"schema_version"`
	AccountID     string                                  `json:"account_id"`
	Revision      uint64                                  `json:"revision"`
	DisplayName   string                                  `json:"display_name"`
	Enabled       bool                                    `json:"enabled"`
	Priority      int                                     `json:"priority"`
	DefaultFor    []accountv1.Capability                  `json:"default_for"`
	Services      map[accountv1.Capability]accountBinding `json:"services"`
	Metadata      json.RawMessage                         `json:"metadata,omitempty"`
}

func ValidateAccountFile(path string) ([]accountv1.ValidationReport, error) {
	accounts, err := LoadAccountFile(path)
	if err != nil {
		return nil, err
	}
	reports := make([]accountv1.ValidationReport, 0, len(accounts))
	for _, account := range accounts {
		reports = append(reports, accountapp.ValidateCreate(account))
	}
	return reports, nil
}

type accountBinding struct {
	Capability      accountv1.Capability `json:"capability"`
	Provider        string               `json:"provider"`
	Protocol        string               `json:"protocol"`
	BaseURL         string               `json:"base_url"`
	Path            string               `json:"path"`
	Model           string               `json:"model"`
	APIKeyEnv       string               `json:"api_key_env,omitempty"`
	CredentialRef   string               `json:"credential_ref"`
	Thinking        *accountThinking     `json:"thinking,omitempty"`
	ReasoningEffort string               `json:"reasoning_effort,omitempty"`
	Dimensions      int                  `json:"dimensions"`
	BatchSize       int                  `json:"batch_size"`
	Normalize       bool                 `json:"normalize"`
	TopN            int                  `json:"top_n"`
	MaxCandidates   int                  `json:"max_candidates"`
	TimeoutMS       int64                `json:"timeout_ms"`
	Timeout         string               `json:"timeout"`
	Capabilities    []string             `json:"capabilities,omitempty"`
	Retry           json.RawMessage      `json:"retry,omitempty"`
	Limits          json.RawMessage      `json:"limits,omitempty"`
	Probe           json.RawMessage      `json:"probe,omitempty"`
}

type accountThinking struct {
	Type string `json:"type"`
}

func LoadAccountFile(path string) ([]accountv1.CreateAccount, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var result []accountv1.CreateAccount
	seen := make(map[string]bool)
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var record accountFileRecord
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("line %d: trailing JSON value", line)
		}
		if record.SchemaVersion != 1 {
			return nil, fmt.Errorf("line %d: unsupported schema version", line)
		}
		if record.Revision != 1 {
			return nil, fmt.Errorf("line %d: imported account revision must be 1", line)
		}
		if seen[record.AccountID] {
			return nil, fmt.Errorf("line %d: duplicate account %q", line, record.AccountID)
		}
		seen[record.AccountID] = true
		services := make(map[accountv1.Capability]accountv1.BindingInput, len(record.Services))
		for capability, binding := range record.Services {
			if binding.Capability == "" {
				binding.Capability = capability
			}
			input := accountv1.BindingInput{
				Capability: binding.Capability, Provider: binding.Provider,
				Protocol: binding.Protocol, BaseURL: binding.BaseURL, Path: binding.Path,
				Model: binding.Model, APIKeyEnv: binding.APIKeyEnv,
				CredentialRef:   binding.CredentialRef,
				ReasoningEffort: binding.ReasoningEffort,
				Dimensions:      binding.Dimensions, BatchSize: binding.BatchSize,
				Normalize: binding.Normalize, TopN: binding.TopN,
				MaxCandidates: binding.MaxCandidates,
				TimeoutMS:     accountTimeoutMS(binding.TimeoutMS, binding.Timeout),
			}
			if binding.Thinking != nil {
				input.Thinking = &accountv1.ThinkingConfig{Type: binding.Thinking.Type}
			}
			services[capability] = normalizeBindingInput(capability, input)
		}
		if chat, ok := services[accountv1.CapabilityChat]; ok &&
			chat.Provider == "deepseek" {
			if _, exists := services[accountv1.CapabilityToolCall]; !exists {
				toolCall := chat
				toolCall.Capability = accountv1.CapabilityToolCall
				services[accountv1.CapabilityToolCall] = toolCall
			}
		}
		result = append(result, accountv1.CreateAccount{
			AccountID: record.AccountID, DisplayName: record.DisplayName,
			Enabled: record.Enabled, Priority: record.Priority,
			DefaultFor: record.DefaultFor, Services: services,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AccountID < result[j].AccountID })
	return result, nil
}

func normalizeBindingInput(capability accountv1.Capability, binding accountv1.BindingInput) accountv1.BindingInput {
	if capability != accountv1.CapabilityChat && capability != accountv1.CapabilityToolCall {
		return binding
	}
	if binding.Thinking != nil {
		binding.Thinking.Type = domain.NormalizeThinkingType(binding.Thinking.Type)
	}
	binding.ReasoningEffort = domain.NormalizeReasoningEffort(binding.ReasoningEffort)
	binding.Provider = strings.ToLower(strings.TrimSpace(binding.Provider))
	if binding.Provider == "" {
		binding.Provider = "deepseek"
	}
	if binding.Provider != "deepseek" {
		return binding
	}
	if binding.Protocol == "" {
		binding.Protocol = domain.DeepSeekOpenAIProtocol
	}
	if binding.BaseURL == "" {
		binding.BaseURL = domain.DeepSeekBaseURL
	}
	if binding.Path == "" {
		binding.Path = domain.DeepSeekChatPath
	}
	if binding.Model == "" {
		binding.Model = domain.DeepSeekFlashModel
	}
	if binding.APIKeyEnv == "" && binding.CredentialRef == "" {
		binding.APIKeyEnv = domain.DeepSeekAPIKeyEnv
	}
	if binding.TimeoutMS <= 0 {
		binding.TimeoutMS = domain.DefaultChatTimeoutMillis
	}
	if binding.Thinking == nil {
		if binding.ReasoningEffort == "none" {
			binding.Thinking = &accountv1.ThinkingConfig{Type: "disabled"}
			binding.ReasoningEffort = ""
		} else {
			binding.Thinking = &accountv1.ThinkingConfig{Type: "enabled"}
		}
	}
	if binding.Thinking.Type == "disabled" && binding.ReasoningEffort == "none" {
		binding.ReasoningEffort = ""
	}
	if binding.Thinking.Type == "enabled" && binding.ReasoningEffort == "" {
		binding.ReasoningEffort = "high"
	}
	return binding
}

func accountTimeoutMS(milliseconds int64, duration string) int64 {
	if milliseconds > 0 || strings.TrimSpace(duration) == "" {
		return milliseconds
	}
	parsed, err := time.ParseDuration(duration)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed.Milliseconds()
}

func summarizeBindings(bindings []accountv1.BindingView) string {
	values := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		value := string(binding.Capability) + ":" + binding.Model
		if binding.Thinking != nil && binding.Thinking.Type != "" {
			value += ",thinking=" + binding.Thinking.Type
		}
		if binding.ReasoningEffort != "" {
			value += ",effort=" + binding.ReasoningEffort
		}
		if binding.Dimensions > 0 {
			value += fmt.Sprintf(",dimensions=%d", binding.Dimensions)
		}
		if binding.BatchSize > 0 {
			value += fmt.Sprintf(",batch=%d", binding.BatchSize)
		}
		if binding.TopN > 0 {
			value += fmt.Sprintf(",top_n=%d,max=%d", binding.TopN, binding.MaxCandidates)
		}
		values = append(values, value)
	}
	return strings.Join(values, ";")
}
