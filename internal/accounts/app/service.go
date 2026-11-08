package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"vibe-agent/internal/accounts/domain"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

type Service struct {
	repository Repository
	leases     LeaseIssuer
	smoke      SmokeRunner
	audit      AuditWriter
	now        func() time.Time
}

func NewService(repository Repository, leases LeaseIssuer, smoke SmokeRunner, audit AuditWriter) *Service {
	return &Service{repository: repository, leases: leases, smoke: smoke, audit: audit, now: time.Now}
}

func (s *Service) Get(ctx context.Context, ref accountv1.AccountRef) (accountv1.AccountView, error) {
	account, err := s.repository.Get(ctx, ref.AccountID)
	if err != nil {
		return accountv1.AccountView{}, mapRepositoryError(err)
	}
	if ref.Revision != 0 && ref.Revision != account.Revision {
		return accountv1.AccountView{}, contractError(accountv1.ErrorConflict, "account revision does not match")
	}
	return view(account), nil
}

func (s *Service) ResolveForInvocation(ctx context.Context, req accountv1.ResolveRequest) (accountv1.ResolvedInvocation, error) {
	accounts, err := s.repository.List(ctx)
	if err != nil {
		return accountv1.ResolvedInvocation{}, mapRepositoryError(err)
	}
	account, binding, err := selectAccount(accounts, req)
	if err != nil {
		return accountv1.ResolvedInvocation{}, err
	}
	if s.leases == nil {
		return accountv1.ResolvedInvocation{}, contractError(accountv1.ErrorDependency, "credential lease issuer is unavailable")
	}
	capability := domain.Capability(req.Capability)
	issued, err := s.leases.Issue(ctx, account, capability, req.Purpose)
	if err != nil {
		return accountv1.ResolvedInvocation{}, contractError(accountv1.ErrorMissingCredential, "credential is unavailable")
	}
	return accountv1.ResolvedInvocation{
		Account:   accountv1.AccountRef{AccountID: account.ID, Revision: account.Revision},
		Binding:   bindingView(binding),
		LeaseID:   issued.LeaseID,
		ExpiresAt: issued.ExpiresAt,
	}, nil
}

func (s *Service) List(ctx context.Context, filter accountv1.ListFilter) ([]accountv1.AccountView, error) {
	accounts, err := s.repository.List(ctx)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	result := make([]accountv1.AccountView, 0, len(accounts))
	for _, account := range accounts {
		if filter.Enabled != nil && account.Enabled != *filter.Enabled {
			continue
		}
		if filter.Capability != "" {
			if _, ok := account.Services[domain.Capability(filter.Capability)]; !ok {
				continue
			}
		}
		result = append(result, view(account))
	}
	return result, nil
}

func (s *Service) Create(ctx context.Context, cmd accountv1.CreateAccount) (accountv1.AccountView, error) {
	account := domain.Account{
		ID:          cmd.AccountID,
		Revision:    1,
		Enabled:     cmd.Enabled,
		DisplayName: cmd.DisplayName,
		Priority:    cmd.Priority,
		DefaultFor:  make(map[domain.Capability]bool),
		Services:    mapBindings(cmd.Services),
	}
	for _, capability := range cmd.DefaultFor {
		account.DefaultFor[domain.Capability(capability)] = true
	}
	if err := account.Validate(); err != nil {
		return accountv1.AccountView{}, contractError(accountv1.ErrorInvalidRequest, err.Error())
	}
	if err := s.repository.Save(ctx, account, 0); err != nil {
		return accountv1.AccountView{}, mapRepositoryError(err)
	}
	if err := s.auditWrite(ctx, AuditEntry{
		Action: "created", AccountID: account.ID, ResultRevision: account.Revision,
		ChangedFields: []string{"account"},
	}); err != nil {
		return view(account), err
	}
	return view(account), nil
}

func (s *Service) Update(ctx context.Context, cmd accountv1.UpdateAccount) (accountv1.AccountView, error) {
	current, err := s.repository.Get(ctx, cmd.Ref.AccountID)
	if err != nil {
		return accountv1.AccountView{}, mapRepositoryError(err)
	}
	before := current.Revision
	current.Revision++
	current.DisplayName = cmd.DisplayName
	current.Priority = cmd.Priority
	current.Services = mapBindings(cmd.Services)
	current.DefaultFor = make(map[domain.Capability]bool)
	for _, capability := range cmd.DefaultFor {
		current.DefaultFor[domain.Capability(capability)] = true
	}
	if err := current.Validate(); err != nil {
		return accountv1.AccountView{}, contractError(accountv1.ErrorInvalidRequest, err.Error())
	}
	if err := s.repository.Save(ctx, current, before); err != nil {
		return accountv1.AccountView{}, mapRepositoryError(err)
	}
	if err := s.auditWrite(ctx, AuditEntry{
		Action: "updated", AccountID: current.ID, ExpectedRevision: before,
		ResultRevision: current.Revision, ChangedFields: []string{"display_name", "priority", "default_for", "services"},
	}); err != nil {
		return view(current), err
	}
	return view(current), nil
}

func (s *Service) SetDefault(ctx context.Context, cmd accountv1.SetDefault) (accountv1.AccountView, error) {
	account, err := s.mutate(ctx, cmd.Ref, func(account *domain.Account) error {
		account.DefaultFor[domain.Capability(cmd.Capability)] = true
		return nil
	})
	if err != nil {
		return accountv1.AccountView{}, err
	}
	if err := s.auditWrite(ctx, AuditEntry{
		Action: "default_set", AccountID: account.ID, ExpectedRevision: cmd.Ref.Revision,
		ResultRevision: account.Revision, ChangedFields: []string{"default_for." + string(cmd.Capability)},
	}); err != nil {
		return view(account), err
	}
	return view(account), nil
}

func (s *Service) SetEnabled(ctx context.Context, cmd accountv1.SetEnabled) (accountv1.AccountView, error) {
	account, err := s.mutate(ctx, cmd.Ref, func(account *domain.Account) error {
		account.Enabled = cmd.Enabled
		return nil
	})
	if err != nil {
		return accountv1.AccountView{}, err
	}
	if err := s.auditWrite(ctx, AuditEntry{
		Action: "enabled_set", AccountID: account.ID, ExpectedRevision: cmd.Ref.Revision,
		ResultRevision: account.Revision, ChangedFields: []string{"enabled"},
	}); err != nil {
		return view(account), err
	}
	return view(account), nil
}

func (s *Service) RotateCredentialRef(ctx context.Context, cmd accountv1.RotateCredentialRef) (accountv1.AccountView, error) {
	account, err := s.mutate(ctx, cmd.Ref, func(account *domain.Account) error {
		binding, ok := account.Services[domain.Capability(cmd.Capability)]
		if !ok {
			return contractError(accountv1.ErrorNotFound, "account binding not found")
		}
		binding.CredentialRef = cmd.CredentialRef
		account.Services[domain.Capability(cmd.Capability)] = binding
		return nil
	})
	if err != nil {
		return accountv1.AccountView{}, err
	}
	if err := s.auditWrite(ctx, AuditEntry{
		Action: "credential_ref_rotated", AccountID: account.ID, ExpectedRevision: cmd.Ref.Revision,
		ResultRevision: account.Revision, ChangedFields: []string{"services." + string(cmd.Capability) + ".credential_ref"},
	}); err != nil {
		return view(account), err
	}
	return view(account), nil
}

func (s *Service) Archive(ctx context.Context, ref accountv1.AccountRef) error {
	account, err := s.mutate(ctx, ref, func(account *domain.Account) error {
		account.Archived = true
		account.Enabled = false
		account.DefaultFor = make(map[domain.Capability]bool)
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.auditWrite(ctx, AuditEntry{
		Action: "archived", AccountID: account.ID, ExpectedRevision: ref.Revision,
		ResultRevision: account.Revision, ChangedFields: []string{"archived", "enabled", "default_for"},
	}); err != nil {
		return err
	}
	return nil
}

func (s *Service) Validate(ctx context.Context, cmd accountv1.ValidateAccount) (accountv1.ValidationReport, error) {
	account, err := s.repository.Get(ctx, cmd.Ref.AccountID)
	if err != nil {
		return accountv1.ValidationReport{Valid: false, Issues: []accountv1.ValidationIssue{{Code: "not_found", Message: err.Error()}}}, nil
	}
	if err := account.Validate(); err != nil {
		return accountv1.ValidationReport{Valid: false, Issues: []accountv1.ValidationIssue{{Code: "invalid_account", Message: err.Error()}}}, nil
	}
	return accountv1.ValidationReport{Valid: true}, nil
}

func (s *Service) Smoke(ctx context.Context, cmd accountv1.SmokeRequest) (accountv1.SmokeReport, error) {
	account, err := s.repository.Get(ctx, cmd.Account.AccountID)
	if err != nil {
		return accountv1.SmokeReport{}, mapRepositoryError(err)
	}
	started := time.Now()
	report := accountv1.SmokeReport{
		CaseID: cmd.CaseID, AccountID: account.ID, Capability: cmd.Capability,
	}
	if s.smoke == nil {
		report.ErrorCode = "smoke_not_configured"
		report.DurationMS = time.Since(started).Milliseconds()
		return report, nil
	}
	if err := s.smoke.Run(ctx, account, domain.Capability(cmd.Capability), cmd.CaseID, cmd.Input, cmd.TimeoutMS); err != nil {
		report.ErrorCode = stableSmokeCode(err)
	} else {
		report.Passed = true
	}
	report.DurationMS = time.Since(started).Milliseconds()
	if err := s.auditWrite(ctx, AuditEntry{Action: "smoke_tested", AccountID: account.ID, ResultRevision: account.Revision}); err != nil {
		return report, err
	}
	return report, nil
}

func (s *Service) mutate(ctx context.Context, ref accountv1.AccountRef, mutate func(*domain.Account) error) (domain.Account, error) {
	account, err := s.repository.Get(ctx, ref.AccountID)
	if err != nil {
		return domain.Account{}, mapRepositoryError(err)
	}
	before := account.Revision
	account.Revision++
	if err := mutate(&account); err != nil {
		return domain.Account{}, err
	}
	if err := account.Validate(); err != nil {
		return domain.Account{}, contractError(accountv1.ErrorInvalidRequest, err.Error())
	}
	if err := s.repository.Save(ctx, account, before); err != nil {
		return domain.Account{}, mapRepositoryError(err)
	}
	return account, nil
}

func (s *Service) auditWrite(ctx context.Context, entry AuditEntry) error {
	if s.audit == nil {
		return nil
	}
	entry.Timestamp = s.now().UTC()
	entry.RequestID = requestID(ctx)
	if actorID, ok := ctx.Value(actorIDKey{}).(string); ok {
		entry.ActorID = actorID
	}
	if entry.ActorID == "" {
		entry.ActorID = "internal"
	}
	return s.audit.Write(ctx, entry)
}

func requestID(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey{}).(string); ok {
		return value
	}
	return ""
}

type requestIDKey struct{}
type actorIDKey struct{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// WithAuditIdentity attaches the authenticated operator identity and request
// correlation id used by account write audit records.
func WithAuditIdentity(ctx context.Context, actorID, requestID string) context.Context {
	ctx = context.WithValue(ctx, actorIDKey{}, actorID)
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

func selectAccount(accounts []domain.Account, req accountv1.ResolveRequest) (domain.Account, domain.Binding, error) {
	capability := domain.Capability(req.Capability)
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].DefaultFor[capability] != accounts[j].DefaultFor[capability] {
			return accounts[i].DefaultFor[capability]
		}
		return accounts[i].Priority > accounts[j].Priority
	})
	for _, account := range accounts {
		if req.PreferredID != "" && account.ID != req.PreferredID {
			continue
		}
		if !account.Enabled || account.Archived {
			continue
		}
		binding, ok := account.Services[capability]
		if !ok || (req.RequiredModel != "" && binding.Model != req.RequiredModel) {
			continue
		}
		return account, binding, nil
	}
	return domain.Account{}, domain.Binding{}, contractError(accountv1.ErrorNotFound, "no available account for capability")
}

func mapBindings(input map[accountv1.Capability]accountv1.BindingInput) map[domain.Capability]domain.Binding {
	result := make(map[domain.Capability]domain.Binding, len(input))
	for capability, value := range input {
		bindingCapability := value.Capability
		if bindingCapability == "" {
			bindingCapability = capability
		}
		credentialRef := value.CredentialRef
		if credentialRef == "" && value.APIKeyEnv != "" {
			credentialRef = "env:" + value.APIKeyEnv
		}
		thinkingType := ""
		if value.Thinking != nil {
			thinkingType = value.Thinking.Type
		}
		result[domain.Capability(capability)] = domain.Binding{
			Capability:      domain.Capability(bindingCapability),
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
			TimeoutMS:       value.TimeoutMS,
		}
	}
	return domain.NormalizeServices(result)
}

func view(account domain.Account) accountv1.AccountView {
	capabilities := make([]accountv1.Capability, 0, len(account.Services))
	defaults := make([]accountv1.Capability, 0, len(account.DefaultFor))
	for capability := range account.Services {
		capabilities = append(capabilities, accountv1.Capability(capability))
	}
	for capability, selected := range account.DefaultFor {
		if selected {
			defaults = append(defaults, accountv1.Capability(capability))
		}
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i] < capabilities[j] })
	sort.Slice(defaults, func(i, j int) bool { return defaults[i] < defaults[j] })
	bindings := make([]accountv1.BindingView, 0, len(capabilities))
	for _, capability := range capabilities {
		bindings = append(bindings, bindingView(account.Services[domain.Capability(capability)]))
	}
	status := "disabled"
	if account.Archived {
		status = "archived"
	} else if account.Enabled {
		status = "enabled"
	}
	return accountv1.AccountView{
		Ref:          accountv1.AccountRef{AccountID: account.ID, Revision: account.Revision},
		DisplayName:  account.DisplayName,
		Enabled:      account.Enabled,
		Status:       status,
		DefaultFor:   defaults,
		Capabilities: capabilities,
		Bindings:     bindings,
	}
}

func bindingView(binding domain.Binding) accountv1.BindingView {
	view := accountv1.BindingView{
		Capability:      accountv1.Capability(binding.Capability),
		Provider:        binding.Provider,
		Protocol:        binding.Protocol,
		BaseURL:         binding.BaseURL,
		Path:            binding.Path,
		Model:           binding.Model,
		Dimensions:      binding.Dimensions,
		BatchSize:       binding.BatchSize,
		Normalize:       binding.Normalize,
		TopN:            binding.TopN,
		MaxCandidates:   binding.MaxCandidates,
		TimeoutMS:       binding.TimeoutMS,
		ReasoningEffort: binding.ReasoningEffort,
	}
	if binding.ThinkingType != "" {
		view.Thinking = &accountv1.ThinkingConfig{Type: binding.ThinkingType}
	}
	return view
}

func mapRepositoryError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return contractError(accountv1.ErrorNotFound, "account not found")
	}
	if strings.Contains(err.Error(), "revision conflict") {
		return contractError(accountv1.ErrorConflict, "account revision conflict")
	}
	return contractError(accountv1.ErrorDependency, "account store is unavailable")
}

func contractError(code accountv1.ErrorCode, message string) error {
	return &accountv1.ContractError{Code: code, Message: message, Retryable: code == accountv1.ErrorDependency}
}

func stableSmokeCode(err error) string {
	var contractErr *accountv1.ContractError
	if errors.As(err, &contractErr) {
		return string(contractErr.Code)
	}
	return "internal_error"
}

var _ accountv1.Admin = (*Service)(nil)

func (s *Service) String() string {
	return fmt.Sprintf("account service")
}
