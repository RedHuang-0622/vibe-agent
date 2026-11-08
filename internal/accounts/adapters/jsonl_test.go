package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"vibe-agent/internal/accounts/domain"
)

func TestJSONLRepositoryRevisionAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.jsonl")
	repository, err := OpenJSONLRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	account := domain.Account{
		ID:          "chat-main",
		Revision:    1,
		Enabled:     true,
		DisplayName: "Chat",
		Services: map[domain.Capability]domain.Binding{
			domain.CapabilityChat: {
				Capability:    domain.CapabilityChat,
				Provider:      "fake",
				Protocol:      "openai_compatible",
				BaseURL:       "http://127.0.0.1:1",
				Path:          "/chat",
				Model:         "model",
				CredentialRef: "env:TEST_KEY",
				TimeoutMS:     1000,
			},
		},
	}
	if err := repository.Save(context.Background(), account, 0); err != nil {
		t.Fatal(err)
	}
	account.Revision = 2
	account.DisplayName = "Chat 2"
	if err := repository.Save(context.Background(), account, 1); err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(context.Background(), account, 1); err == nil {
		t.Fatal("stale revision must fail")
	}
	reopened, err := OpenJSONLRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(context.Background(), "chat-main")
	if err != nil || got.Revision != 2 || got.DisplayName != "Chat 2" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestJSONLRepositoryAcceptsDurationTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.jsonl")
	record := `{"schema_version":1,"account_id":"chat-main","revision":1,"enabled":true,"display_name":"Chat","services":{"chat":{"provider":"fake","protocol":"openai_compatible","base_url":"https://example.test","path":"/chat","model":"model","credential_ref":"env:TEST_KEY","timeout":"120s"}}}`
	if err := os.WriteFile(path, []byte(record+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenJSONLRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	account, err := repository.Get(context.Background(), "chat-main")
	if err != nil {
		t.Fatal(err)
	}
	if account.Services[domain.CapabilityChat].TimeoutMS != 120000 {
		t.Fatalf("timeout=%d", account.Services[domain.CapabilityChat].TimeoutMS)
	}
}

func TestJSONLRepositoryRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.jsonl")
	record := `{"schema_version":1,"account_id":"chat-main","revision":1,"enabled":true,"display_name":"Chat","unexpected":true,"services":{"chat":{"provider":"fake","protocol":"openai_compatible","base_url":"https://example.test","path":"/chat","model":"model","credential_ref":"env:TEST_KEY","timeout":"1s"}}}`
	if err := os.WriteFile(path, []byte(record+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJSONLRepository(path); err == nil {
		t.Fatal("unknown account field must be rejected")
	}
}

func TestJSONLRepositoryLoadsDeepSeekThinkingDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.jsonl")
	record := `{"schema_version":1,"account_id":"deepseek","revision":1,"enabled":true,"display_name":"DeepSeek","services":{"chat":{"api_key_env":"DEEPSEEK_API_KEY","thinking":{"type":"enabled"},"reasoning_effort":"high"}}}`
	if err := os.WriteFile(path, []byte(record+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenJSONLRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	account, err := repository.Get(context.Background(), "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	binding := account.Services[domain.CapabilityChat]
	if binding.Model != "deepseek-flash" ||
		binding.Protocol != "openai_compatible" ||
		binding.BaseURL != "https://api.deepseek.com" ||
		binding.Path != "/chat/completions" ||
		binding.CredentialRef != "env:DEEPSEEK_API_KEY" ||
		binding.ThinkingType != "enabled" ||
		binding.ReasoningEffort != "high" {
		t.Fatalf("binding=%#v", binding)
	}
}

func TestJSONLRepositoryMinimalDeepSeekChatBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.jsonl")
	record := `{"schema_version":1,"account_id":"deepseek","revision":1,"enabled":true,"display_name":"DeepSeek","services":{"chat":{}}}`
	if err := os.WriteFile(path, []byte(record+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenJSONLRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	account, err := repository.Get(context.Background(), "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	binding := account.Services[domain.CapabilityChat]
	if binding.Model != domain.DeepSeekFlashModel ||
		binding.CredentialRef != "env:DEEPSEEK_API_KEY" ||
		binding.ThinkingType != "enabled" ||
		binding.ReasoningEffort != "high" {
		t.Fatalf("binding=%#v", binding)
	}
}
