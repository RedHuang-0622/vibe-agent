package accountv1

import "time"

const (
	ContractName = "vibe.account"
	Version      = 1
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

type AccountRef struct {
	AccountID string `json:"account_id"`
	Revision  uint64 `json:"revision"`
}

type ThinkingConfig struct {
	Type string `json:"type"`
}

type BindingView struct {
	Capability      Capability      `json:"capability"`
	Provider        string          `json:"provider"`
	Protocol        string          `json:"protocol"`
	BaseURL         string          `json:"base_url"`
	Path            string          `json:"path"`
	Model           string          `json:"model"`
	Thinking        *ThinkingConfig `json:"thinking,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Dimensions      int             `json:"dimensions,omitempty"`
	BatchSize       int             `json:"batch_size,omitempty"`
	Normalize       bool            `json:"normalize,omitempty"`
	TopN            int             `json:"top_n,omitempty"`
	MaxCandidates   int             `json:"max_candidates,omitempty"`
	TimeoutMS       int64           `json:"timeout_ms"`
}

type AccountView struct {
	Ref          AccountRef    `json:"ref"`
	DisplayName  string        `json:"display_name"`
	Enabled      bool          `json:"enabled"`
	Status       string        `json:"status"`
	DefaultFor   []Capability  `json:"default_for,omitempty"`
	Capabilities []Capability  `json:"capabilities"`
	Bindings     []BindingView `json:"bindings,omitempty"`
}

type ResolveRequest struct {
	Capability    Capability `json:"capability"`
	PreferredID   string     `json:"preferred_account_id,omitempty"`
	RequiredModel string     `json:"required_model,omitempty"`
	Purpose       string     `json:"purpose"`
}

type ResolvedInvocation struct {
	Account   AccountRef  `json:"account"`
	Binding   BindingView `json:"binding"`
	LeaseID   string      `json:"credential_lease_id"`
	ExpiresAt time.Time   `json:"expires_at"`
}

type ListFilter struct {
	Capability Capability `json:"capability,omitempty"`
	Enabled    *bool      `json:"enabled,omitempty"`
}
