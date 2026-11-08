package accountv1

import "context"

type BindingInput struct {
	Capability      Capability      `json:"capability"`
	Provider        string          `json:"provider,omitempty"`
	Protocol        string          `json:"protocol,omitempty"`
	BaseURL         string          `json:"base_url,omitempty"`
	Path            string          `json:"path,omitempty"`
	Model           string          `json:"model,omitempty"`
	APIKeyEnv       string          `json:"api_key_env,omitempty"`
	CredentialRef   string          `json:"credential_ref,omitempty"`
	Thinking        *ThinkingConfig `json:"thinking,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Dimensions      int             `json:"dimensions,omitempty"`
	BatchSize       int             `json:"batch_size,omitempty"`
	Normalize       bool            `json:"normalize,omitempty"`
	TopN            int             `json:"top_n,omitempty"`
	MaxCandidates   int             `json:"max_candidates,omitempty"`
	TimeoutMS       int64           `json:"timeout_ms,omitempty"`
}

type CreateAccount struct {
	AccountID   string                      `json:"account_id"`
	DisplayName string                      `json:"display_name"`
	Enabled     bool                        `json:"enabled"`
	Priority    int                         `json:"priority"`
	DefaultFor  []Capability                `json:"default_for,omitempty"`
	Services    map[Capability]BindingInput `json:"services"`
}

type UpdateAccount struct {
	Ref         AccountRef                  `json:"ref"`
	DisplayName string                      `json:"display_name"`
	Priority    int                         `json:"priority"`
	DefaultFor  []Capability                `json:"default_for,omitempty"`
	Services    map[Capability]BindingInput `json:"services"`
}

type SetDefault struct {
	Ref        AccountRef `json:"ref"`
	Capability Capability `json:"capability"`
}

type SetEnabled struct {
	Ref     AccountRef `json:"ref"`
	Enabled bool       `json:"enabled"`
}

type RotateCredentialRef struct {
	Ref           AccountRef `json:"ref"`
	Capability    Capability `json:"capability"`
	CredentialRef string     `json:"credential_ref"`
}

type ValidateAccount struct {
	Ref AccountRef `json:"ref"`
}

type ValidationIssue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ValidationReport struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues,omitempty"`
}

type SmokeRequest struct {
	Account    AccountRef     `json:"account"`
	Capability Capability     `json:"capability"`
	CaseID     string         `json:"case_id"`
	Input      map[string]any `json:"input,omitempty"`
	TimeoutMS  int64          `json:"timeout_ms,omitempty"`
}

type SmokeReport struct {
	CaseID     string     `json:"case_id"`
	AccountID  string     `json:"account_id"`
	Capability Capability `json:"capability"`
	Passed     bool       `json:"passed"`
	ErrorCode  string     `json:"error_code,omitempty"`
	DurationMS int64      `json:"duration_ms"`
}

type Admin interface {
	Reader
	List(ctx context.Context, filter ListFilter) ([]AccountView, error)
	Create(ctx context.Context, cmd CreateAccount) (AccountView, error)
	Update(ctx context.Context, cmd UpdateAccount) (AccountView, error)
	SetDefault(ctx context.Context, cmd SetDefault) (AccountView, error)
	SetEnabled(ctx context.Context, cmd SetEnabled) (AccountView, error)
	RotateCredentialRef(ctx context.Context, cmd RotateCredentialRef) (AccountView, error)
	Archive(ctx context.Context, ref AccountRef) error
	Validate(ctx context.Context, cmd ValidateAccount) (ValidationReport, error)
	Smoke(ctx context.Context, cmd SmokeRequest) (SmokeReport, error)
}
