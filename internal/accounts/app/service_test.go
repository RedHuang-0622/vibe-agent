package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibe-agent/internal/accounts/adapters"
	"vibe-agent/internal/accounts/app"
	"vibe-agent/internal/accounts/domain"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

func TestAccountAdminResolveRevisionAndAudit(t *testing.T) {
	t.Setenv("TEST_CHAT_KEY", "top-secret-value")
	root := t.TempDir()
	repository, err := adapters.OpenJSONLRepository(filepath.Join(root, "accounts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	leaseManager := app.NewLeaseManager(time.Minute)
	audit := adapters.NewFileAudit(filepath.Join(root, "audit.jsonl"))
	service := app.NewService(repository, leaseManager, nil, audit)
	ctx := app.WithAuditIdentity(context.Background(), "operator:test", "req-1")
	created, err := service.Create(ctx, accountv1.CreateAccount{
		AccountID:   "chat-main",
		DisplayName: "Chat Main",
		Enabled:     true,
		Priority:    10,
		DefaultFor:  []accountv1.Capability{accountv1.CapabilityChat},
		Services: map[accountv1.Capability]accountv1.BindingInput{
			accountv1.CapabilityChat: {
				Capability:    accountv1.CapabilityChat,
				Provider:      "fake",
				Protocol:      "openai_compatible",
				BaseURL:       "http://127.0.0.1:1",
				Path:          "/chat/completions",
				Model:         "chat-model",
				CredentialRef: "env:TEST_CHAT_KEY",
				TimeoutMS:     1000,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Ref.Revision != 1 {
		t.Fatalf("revision=%d", created.Ref.Revision)
	}
	resolved, err := service.ResolveForInvocation(context.Background(), accountv1.ResolveRequest{
		Capability: accountv1.CapabilityChat,
		Purpose:    "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Binding.Model != "chat-model" || resolved.LeaseID == "" {
		t.Fatalf("resolved=%#v", resolved)
	}
	secret, err := leaseManager.Redeem(context.Background(), resolved.LeaseID, "chat")
	if err != nil || string(secret) != "top-secret-value" {
		t.Fatalf("secret=%q err=%v", secret, err)
	}
	if _, err := leaseManager.Redeem(context.Background(), resolved.LeaseID, "chat"); err == nil {
		t.Fatal("lease must be one-time")
	}
	updated, err := service.Update(context.Background(), accountv1.UpdateAccount{
		Ref:         created.Ref,
		DisplayName: "Chat Main 2",
		Priority:    20,
		DefaultFor:  []accountv1.Capability{accountv1.CapabilityChat},
		Services: map[accountv1.Capability]accountv1.BindingInput{
			accountv1.CapabilityChat: {
				Capability:    accountv1.CapabilityChat,
				Provider:      "fake",
				Protocol:      "openai_compatible",
				BaseURL:       "http://127.0.0.1:1",
				Path:          "/chat/completions",
				Model:         "chat-model",
				CredentialRef: "env:TEST_CHAT_KEY",
				TimeoutMS:     1000,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Ref.Revision != 2 {
		t.Fatalf("updated revision=%d", updated.Ref.Revision)
	}
	if _, err := service.Update(context.Background(), accountv1.UpdateAccount{Ref: created.Ref}); err == nil {
		t.Fatal("stale revision must conflict")
	}
	auditData, err := os.ReadFile(filepath.Join(root, "audit.jsonl"))
	if err != nil || len(auditData) == 0 {
		t.Fatalf("audit=%q err=%v", auditData, err)
	}
	if stringContains(auditData, "top-secret-value") {
		t.Fatal("audit leaked credential")
	}
	if !stringContains(auditData, `"actor_id":"operator:test"`) ||
		!stringContains(auditData, `"request_id":"req-1"`) {
		t.Fatalf("audit identity missing: %s", auditData)
	}
}

func TestLeaseExpiry(t *testing.T) {
	t.Setenv("TEST_KEY", "secret")
	manager := app.NewLeaseManager(time.Nanosecond)
	account := domain.Account{
		ID:          "chat-main",
		Revision:    1,
		Enabled:     true,
		DisplayName: "Chat Main",
		Services: map[domain.Capability]domain.Binding{
			domain.CapabilityChat: {
				Capability:    domain.CapabilityChat,
				Provider:      "fake",
				Protocol:      "openai_compatible",
				BaseURL:       "http://127.0.0.1:1",
				Path:          "/chat/completions",
				Model:         "model",
				CredentialRef: "env:TEST_KEY",
				TimeoutMS:     1000,
			},
		},
	}
	lease, err := manager.Issue(context.Background(), account, "chat", "chat")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := manager.Redeem(context.Background(), lease.LeaseID, "chat"); err == nil {
		t.Fatal("expired lease must fail")
	}
}

func TestDeepSeekAccountDefaultsFromAPIKeyEnvironment(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	repository, err := adapters.OpenJSONLRepository(filepath.Join(t.TempDir(), "accounts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(repository, app.NewLeaseManager(time.Minute), nil, nil)
	created, err := service.Create(context.Background(), accountv1.CreateAccount{
		AccountID: "deepseek", DisplayName: "DeepSeek", Enabled: true,
		DefaultFor: []accountv1.Capability{accountv1.CapabilityChat},
		Services: map[accountv1.Capability]accountv1.BindingInput{
			accountv1.CapabilityChat: {
				Capability: accountv1.CapabilityChat,
				APIKeyEnv:  "DEEPSEEK_API_KEY",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Bindings) != 2 {
		t.Fatalf("bindings=%#v", created.Bindings)
	}
	binding := created.Bindings[0]
	if binding.Model != "deepseek-flash" || binding.Thinking == nil ||
		binding.Thinking.Type != "enabled" || binding.ReasoningEffort != "high" {
		t.Fatalf("binding=%#v", binding)
	}
	toolCall := created.Bindings[1]
	if toolCall.Capability != accountv1.CapabilityToolCall ||
		toolCall.Model != "deepseek-flash" ||
		toolCall.Thinking == nil || toolCall.Thinking.Type != "enabled" {
		t.Fatalf("tool call binding=%#v", toolCall)
	}
	resolved, err := service.ResolveForInvocation(context.Background(), accountv1.ResolveRequest{
		Capability: accountv1.CapabilityChat, Purpose: "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Binding.Model != "deepseek-flash" ||
		resolved.Binding.ReasoningEffort != "high" {
		t.Fatalf("resolved=%#v", resolved)
	}
}

func stringContains(data []byte, value string) bool {
	for i := 0; i+len(value) <= len(data); i++ {
		if string(data[i:i+len(value)]) == value {
			return true
		}
	}
	return false
}
