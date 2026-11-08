package domain

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var accountIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var credentialRefPattern = regexp.MustCompile(`^env:[A-Za-z_][A-Za-z0-9_]*$`)

const (
	DeepSeekFlashModel       = "deepseek-flash"
	DeepSeekBaseURL          = "https://api.deepseek.com"
	DeepSeekChatPath         = "/chat/completions"
	DeepSeekAPIKeyEnv        = "DEEPSEEK_API_KEY"
	DeepSeekOpenAIProtocol   = "openai_compatible"
	DefaultChatTimeoutMillis = 120000
)

type Capability string

const (
	CapabilityChat      Capability = "chat"
	CapabilityToolCall  Capability = "tool_call"
	CapabilityEmbedding Capability = "embedding"
	CapabilityRerank    Capability = "rerank"
	CapabilitySearch    Capability = "search"
)

func (c Capability) Valid() bool {
	switch c {
	case CapabilityChat, CapabilityToolCall, CapabilityEmbedding, CapabilityRerank, CapabilitySearch:
		return true
	default:
		return false
	}
}

type Binding struct {
	Capability      Capability
	Provider        string
	Protocol        string
	BaseURL         string
	Path            string
	Model           string
	CredentialRef   string
	ThinkingType    string
	ReasoningEffort string
	Dimensions      int
	BatchSize       int
	Normalize       bool
	TopN            int
	MaxCandidates   int
	TimeoutMS       int64
}

type Account struct {
	ID          string
	Revision    uint64
	Enabled     bool
	Archived    bool
	DisplayName string
	Priority    int
	DefaultFor  map[Capability]bool
	Services    map[Capability]Binding
}

func (a Account) Validate() error {
	var issues []error
	if !accountIDPattern.MatchString(a.ID) {
		issues = append(issues, errors.New("account id must match ^[a-z0-9][a-z0-9._-]{0,63}$"))
	}
	if a.Revision == 0 {
		issues = append(issues, errors.New("revision must be positive"))
	}
	if a.Archived && a.Enabled {
		issues = append(issues, errors.New("archived account cannot be enabled"))
	}
	if strings.TrimSpace(a.DisplayName) == "" {
		issues = append(issues, errors.New("display name is required"))
	}
	if len(a.Services) == 0 {
		issues = append(issues, errors.New("at least one service binding is required"))
	}
	for capability, binding := range a.Services {
		if !capability.Valid() {
			issues = append(issues, fmt.Errorf("unsupported capability %q", capability))
		}
		if binding.Capability != capability {
			issues = append(issues, fmt.Errorf("%s: binding capability %q does not match service key", capability, binding.Capability))
		}
		if err := validateBinding(binding); err != nil {
			issues = append(issues, fmt.Errorf("%s: %w", capability, err))
		}
	}
	for capability, selected := range a.DefaultFor {
		if !selected {
			continue
		}
		if _, ok := a.Services[capability]; !ok {
			issues = append(issues, fmt.Errorf("default capability %s has no binding", capability))
		}
		if !a.Enabled || a.Archived {
			issues = append(issues, fmt.Errorf("disabled account cannot be default for %s", capability))
		}
	}
	return errors.Join(issues...)
}

func validateBinding(binding Binding) error {
	var issues []error
	required := map[string]string{
		"provider":       binding.Provider,
		"protocol":       binding.Protocol,
		"base url":       binding.BaseURL,
		"path":           binding.Path,
		"model":          binding.Model,
		"credential ref": binding.CredentialRef,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			issues = append(issues, fmt.Errorf("%s is required", field))
		}
	}
	if binding.BaseURL != "" {
		u, err := url.Parse(binding.BaseURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			issues = append(issues, errors.New("base url must be absolute"))
		} else if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
			issues = append(issues, errors.New("base url must use https except for loopback development endpoints"))
		}
	}
	if binding.Path != "" && !strings.HasPrefix(binding.Path, "/") {
		issues = append(issues, errors.New("path must start with /"))
	}
	if binding.CredentialRef != "" && !credentialRefPattern.MatchString(binding.CredentialRef) {
		issues = append(issues, errors.New("credential ref must use env:NAME"))
	}
	if binding.TimeoutMS <= 0 {
		issues = append(issues, errors.New("timeout must be positive"))
	}
	if binding.Capability == CapabilityChat || binding.Capability == CapabilityToolCall {
		if binding.Provider == "deepseek" {
			if binding.Protocol != "openai_compatible" {
				issues = append(issues, errors.New("deepseek protocol must be openai_compatible"))
			}
			if binding.Model != "deepseek-flash" {
				issues = append(issues, errors.New("deepseek model must be deepseek-flash"))
			}
		}
		if binding.ThinkingType != "" &&
			binding.ThinkingType != "enabled" && binding.ThinkingType != "disabled" {
			issues = append(issues, errors.New("thinking type must be enabled or disabled"))
		}
		switch binding.ReasoningEffort {
		case "", "none", "low", "high", "max":
		default:
			issues = append(issues, errors.New("reasoning effort must be none, low, high or max"))
		}
		if binding.ThinkingType == "disabled" && binding.ReasoningEffort != "" &&
			binding.ReasoningEffort != "none" {
			issues = append(issues, errors.New("reasoning effort requires thinking enabled"))
		}
		if binding.ThinkingType == "enabled" && binding.ReasoningEffort == "none" {
			issues = append(issues, errors.New("reasoning effort none conflicts with thinking enabled"))
		}
	} else if binding.ThinkingType != "" || binding.ReasoningEffort != "" {
		issues = append(issues, errors.New("thinking and reasoning effort are only supported for chat/tool_call"))
	}
	if binding.Capability == CapabilityEmbedding {
		if binding.Dimensions <= 0 {
			issues = append(issues, errors.New("embedding dimensions must be positive"))
		}
		if binding.BatchSize <= 0 {
			issues = append(issues, errors.New("embedding batch size must be positive"))
		}
	}
	if binding.Capability == CapabilityRerank {
		if binding.TopN <= 0 {
			issues = append(issues, errors.New("rerank top_n must be positive"))
		}
		if binding.MaxCandidates < binding.TopN {
			issues = append(issues, errors.New("rerank max_candidates must be >= top_n"))
		}
	}
	return errors.Join(issues...)
}

// NormalizeBinding applies the first-party DeepSeek defaults used by chat and
// tool-call capabilities. Explicit non-DeepSeek providers remain untouched.
func NormalizeBinding(capability Capability, binding Binding) Binding {
	if capability != CapabilityChat && capability != CapabilityToolCall {
		return binding
	}
	binding.ThinkingType = NormalizeThinkingType(binding.ThinkingType)
	binding.ReasoningEffort = NormalizeReasoningEffort(binding.ReasoningEffort)
	binding.Provider = strings.ToLower(strings.TrimSpace(binding.Provider))
	if binding.Provider == "" {
		binding.Provider = "deepseek"
	}
	if binding.Provider != "deepseek" {
		return binding
	}
	if binding.Protocol == "" {
		binding.Protocol = DeepSeekOpenAIProtocol
	}
	if binding.BaseURL == "" {
		binding.BaseURL = DeepSeekBaseURL
	}
	if binding.Path == "" {
		binding.Path = DeepSeekChatPath
	}
	if binding.Model == "" {
		binding.Model = DeepSeekFlashModel
	}
	if binding.CredentialRef == "" {
		binding.CredentialRef = "env:" + DeepSeekAPIKeyEnv
	}
	if binding.TimeoutMS <= 0 {
		binding.TimeoutMS = DefaultChatTimeoutMillis
	}
	if binding.ThinkingType == "" {
		if binding.ReasoningEffort == "none" {
			binding.ThinkingType = "disabled"
			binding.ReasoningEffort = ""
		} else {
			binding.ThinkingType = "enabled"
		}
	}
	if binding.ThinkingType == "disabled" && binding.ReasoningEffort == "none" {
		binding.ReasoningEffort = ""
	}
	if binding.ThinkingType == "enabled" && binding.ReasoningEffort == "" {
		binding.ReasoningEffort = "high"
	}
	return binding
}

// NormalizeServices canonicalizes each binding and reuses a DeepSeek chat
// binding for tool calls when no explicit tool_call binding is configured.
func NormalizeServices(input map[Capability]Binding) map[Capability]Binding {
	normalized := make(map[Capability]Binding, len(input)+1)
	for key, binding := range input {
		capability := binding.Capability
		if capability == "" {
			capability = key
		}
		binding.Capability = capability
		normalized[key] = NormalizeBinding(capability, binding)
	}
	chat, ok := normalized[CapabilityChat]
	if !ok || chat.Provider != "deepseek" {
		return normalized
	}
	if _, exists := normalized[CapabilityToolCall]; !exists {
		toolCall := chat
		toolCall.Capability = CapabilityToolCall
		normalized[CapabilityToolCall] = toolCall
	}
	return normalized
}

// NormalizeThinkingType canonicalizes the user-facing DeepSeek mode switch.
func NormalizeThinkingType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// NormalizeReasoningEffort maps DeepSeek compatibility aliases to the
// currently documented effort levels.
func NormalizeReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal":
		return "low"
	case "medium", "xhigh":
		return "high"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
