package domain

import "testing"

func validAccount() Account {
	return Account{
		ID:          "deepseek-main",
		Revision:    1,
		Enabled:     true,
		DisplayName: "DeepSeek Main",
		DefaultFor:  map[Capability]bool{CapabilityChat: true},
		Services: map[Capability]Binding{
			CapabilityChat: {
				Capability:    CapabilityChat,
				Provider:      "deepseek",
				Protocol:      "openai_compatible",
				BaseURL:       "https://api.example.test",
				Path:          "/chat/completions",
				Model:         "deepseek-flash",
				CredentialRef: "env:CHAT_API_KEY",
				TimeoutMS:     1000,
			},
		},
	}
}

func TestDeepSeekBindingDefaultsAndValidation(t *testing.T) {
	binding := NormalizeBinding(CapabilityChat, Binding{
		Capability: CapabilityChat,
	})
	if binding.Provider != "deepseek" || binding.Model != "deepseek-flash" ||
		binding.Protocol != "openai_compatible" ||
		binding.BaseURL != "https://api.deepseek.com" ||
		binding.Path != "/chat/completions" ||
		binding.ThinkingType != "enabled" ||
		binding.ReasoningEffort != "high" ||
		binding.CredentialRef != "env:DEEPSEEK_API_KEY" {
		t.Fatalf("binding=%#v", binding)
	}
	account := validAccount()
	account.Services[CapabilityChat] = binding
	if err := account.Validate(); err != nil {
		t.Fatalf("normalized DeepSeek binding rejected: %v", err)
	}
	binding.Model = "deepseek-chat"
	if err := validateBinding(binding); err == nil {
		t.Fatal("non-flash DeepSeek model must be rejected")
	}
	binding.Model = "deepseek-flash[1m]"
	if err := validateBinding(binding); err == nil {
		t.Fatal("Claude Code model suffix must not be sent to chat completions")
	}
	binding = NormalizeBinding(CapabilityChat, Binding{
		Capability: CapabilityChat, CredentialRef: "env:KEY", ThinkingType: "sometimes",
	})
	if err := validateBinding(binding); err == nil {
		t.Fatal("invalid thinking type must be rejected")
	}
	binding = NormalizeBinding(CapabilityChat, Binding{
		Capability: CapabilityChat, CredentialRef: "env:KEY", ReasoningEffort: "extreme",
	})
	if err := validateBinding(binding); err == nil {
		t.Fatal("invalid reasoning effort must be rejected")
	}
}

func TestDeepSeekReasoningEffortCanonicalization(t *testing.T) {
	tests := []struct {
		name       string
		thinking   string
		effort     string
		wantType   string
		wantEffort string
	}{
		{name: "minimal alias", effort: "minimal", wantType: "enabled", wantEffort: "low"},
		{name: "medium alias", effort: "medium", wantType: "enabled", wantEffort: "high"},
		{name: "xhigh alias", effort: "xhigh", wantType: "enabled", wantEffort: "high"},
		{name: "max", effort: "max", wantType: "enabled", wantEffort: "max"},
		{name: "none disables thinking", effort: "none", wantType: "disabled"},
		{name: "disabled", thinking: "disabled", wantType: "disabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := NormalizeBinding(CapabilityChat, Binding{
				Capability: CapabilityChat, ThinkingType: test.thinking,
				ReasoningEffort: test.effort,
			})
			if binding.ThinkingType != test.wantType || binding.ReasoningEffort != test.wantEffort {
				t.Fatalf("binding=%#v", binding)
			}
			if err := validateBinding(binding); err != nil {
				t.Fatalf("normalized binding rejected: %v", err)
			}
		})
	}

	if err := validateBinding(NormalizeBinding(CapabilityChat, Binding{
		Capability: CapabilityChat, ThinkingType: "enabled", ReasoningEffort: "none",
	})); err == nil {
		t.Fatal("thinking enabled with effort none must be rejected")
	}
	if err := validateBinding(NormalizeBinding(CapabilityChat, Binding{
		Capability: CapabilityChat, ThinkingType: "disabled", ReasoningEffort: "high",
	})); err == nil {
		t.Fatal("thinking disabled with high effort must be rejected")
	}

	services := NormalizeServices(map[Capability]Binding{
		CapabilityChat: {Capability: CapabilityChat},
	})
	toolCall, ok := services[CapabilityToolCall]
	if !ok || toolCall.Model != DeepSeekFlashModel ||
		toolCall.ThinkingType != "enabled" ||
		toolCall.ReasoningEffort != "high" {
		t.Fatalf("tool call binding=%#v", toolCall)
	}
	services = NormalizeServices(map[Capability]Binding{
		CapabilityChat:     {Capability: CapabilityChat},
		CapabilityToolCall: {Capability: CapabilityToolCall, ThinkingType: "disabled"},
	})
	if services[CapabilityToolCall].ThinkingType != "disabled" {
		t.Fatalf("explicit tool_call was overwritten: %#v", services[CapabilityToolCall])
	}
}

func TestDeepSeekProviderNameIsCanonicalized(t *testing.T) {
	binding := NormalizeBinding(CapabilityChat, Binding{
		Capability: CapabilityChat, Provider: " DeepSeek ",
	})
	if binding.Provider != "deepseek" || binding.Model != DeepSeekFlashModel {
		t.Fatalf("binding=%#v", binding)
	}
}

func TestAccountValidate(t *testing.T) {
	account := validAccount()
	if err := account.Validate(); err != nil {
		t.Fatalf("valid account rejected: %v", err)
	}
}

func TestAccountRejectsInvalidID(t *testing.T) {
	account := validAccount()
	account.ID = "../escape"
	if err := account.Validate(); err == nil {
		t.Fatal("invalid account ID should fail validation")
	}
}

func TestAccountRejectsDefaultWithoutBinding(t *testing.T) {
	account := validAccount()
	account.DefaultFor[CapabilityEmbedding] = true
	if err := account.Validate(); err == nil {
		t.Fatal("default capability without binding should fail validation")
	}
}

func TestEmbeddingBindingRequiresDimensions(t *testing.T) {
	account := validAccount()
	account.Services = map[Capability]Binding{
		CapabilityEmbedding: {
			Capability:    CapabilityEmbedding,
			Provider:      "embedding-provider",
			Protocol:      "openai_embeddings_v1",
			BaseURL:       "https://embedding.example.test/v1",
			Path:          "/embeddings",
			Model:         "embedding-model",
			CredentialRef: "env:EMBEDDING_API_KEY",
			BatchSize:     32,
			TimeoutMS:     1000,
		},
	}
	account.DefaultFor = map[Capability]bool{CapabilityEmbedding: true}
	if err := account.Validate(); err == nil {
		t.Fatal("embedding binding without dimensions should fail")
	}
}

func TestAccountRejectsUnsafeEndpointAndCredential(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Account)
	}{
		{
			name: "public http",
			mutate: func(account *Account) {
				binding := account.Services[CapabilityChat]
				binding.BaseURL = "http://example.test"
				account.Services[CapabilityChat] = binding
			},
		},
		{
			name: "unsupported credential scheme",
			mutate: func(account *Account) {
				binding := account.Services[CapabilityChat]
				binding.CredentialRef = "plain-text-secret"
				account.Services[CapabilityChat] = binding
			},
		},
		{
			name: "mismatched binding capability",
			mutate: func(account *Account) {
				binding := account.Services[CapabilityChat]
				binding.Capability = CapabilitySearch
				account.Services[CapabilityChat] = binding
			},
		},
		{
			name: "relative path",
			mutate: func(account *Account) {
				binding := account.Services[CapabilityChat]
				binding.Path = "chat"
				account.Services[CapabilityChat] = binding
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := validAccount()
			test.mutate(&account)
			if err := account.Validate(); err == nil {
				t.Fatal("unsafe account should fail validation")
			}
		})
	}
	if err := validAccount().Validate(); err != nil {
		t.Fatalf("valid account rejected: %v", err)
	}
}
