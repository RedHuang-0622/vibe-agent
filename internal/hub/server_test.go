package hub

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"vibe-agent/internal/accounts/adapters"
	accountapp "vibe-agent/internal/accounts/app"
	"vibe-agent/internal/knowledge"
	"vibe-agent/internal/sessionruntime"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

type managerStub struct {
	created sessionv1.SessionView
}

func TestPublicHubErrorPreservesKnowledgeQuota(t *testing.T) {
	resetAt := time.Now().Add(time.Minute).UTC()
	public := publicHubError(&knowledge.ServiceError{
		Code: "quota_exhausted", Message: "quota", Scope: "provider",
		Provider: "embedding", RequestID: "provider-request", ResetAt: &resetAt,
	})
	if public.Code != "quota_exhausted" || public.ResetAt == nil ||
		public.Provider != "embedding" || public.RequestID != "provider-request" {
		t.Fatalf("public=%#v", public)
	}
}

func (m *managerStub) Create(context.Context, sessionv1.CreateSessionRequest) (sessionv1.SessionView, error) {
	return m.created, nil
}
func (m *managerStub) List(context.Context) ([]sessionv1.SessionView, error) {
	return []sessionv1.SessionView{m.created}, nil
}
func (m *managerStub) Inspect(context.Context, string) (sessionv1.SessionView, error) {
	return m.created, nil
}
func (m *managerStub) Route(_ context.Context, _ string, method string, payload, target any) error {
	if method == sessionv1.MethodSessionHealth {
		raw, _ := json.Marshal(map[string]string{"status": "ok"})
		*(target.(*json.RawMessage)) = raw
	}
	if method == sessionv1.MethodSessionSnapshot {
		raw, _ := json.Marshal(sessionv1.SessionSnapshotResponse{
			SessionID: "session", State: sessionv1.StateIdle, Watermark: 1,
		})
		*(target.(*json.RawMessage)) = raw
	}
	return nil
}
func (m *managerStub) Stop(context.Context, string) error { return nil }

func TestHubTokenScopesAccountReadOnly(t *testing.T) {
	t.Setenv("HUB_TEST_KEY", "secret")
	root := t.TempDir()
	repository, err := adapters.OpenJSONLRepository(filepath.Join(root, "accounts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	leases := accountapp.NewLeaseManager(time.Minute)
	accounts := accountapp.NewService(repository, leases, nil, nil)
	_, err = accounts.Create(context.Background(), accountv1.CreateAccount{
		AccountID: "chat", DisplayName: "Chat", Enabled: true,
		DefaultFor: []accountv1.Capability{accountv1.CapabilityChat},
		Services: map[accountv1.Capability]accountv1.BindingInput{
			accountv1.CapabilityChat: {
				Capability: accountv1.CapabilityChat, Provider: "fake", Protocol: "openai_compatible",
				BaseURL: "http://127.0.0.1:1", Path: "/chat", Model: "model",
				CredentialRef: "env:HUB_TEST_KEY", TimeoutMS: 1000,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener, "admin-token", "agent-token", &managerStub{}, accounts, nil, leases)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve() }()
	defer server.Close(context.Background())

	agent := sessionruntime.NewHubClient("http://"+server.Endpoint(), "agent-token", nil)
	var resolved accountv1.ResolvedInvocation
	if err := agent.Invoke(ctx, accountv1.MethodReadResolve, accountv1.ResolveRequest{
		Capability: accountv1.CapabilityChat, Purpose: "chat",
	}, &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Account.AccountID != "chat" || resolved.LeaseID == "" {
		t.Fatalf("resolved=%#v", resolved)
	}
	err = agent.Invoke(ctx, accountv1.MethodAdminUpdate, accountv1.UpdateAccount{
		Ref: resolved.Account,
	}, &accountv1.AccountView{})
	if err == nil {
		t.Fatal("agent token must not call account update")
	}
	coded, ok := err.(*sessionruntime.CodedError)
	if !ok || coded.Code != "permission_denied" {
		t.Fatalf("err=%T %v", err, err)
	}
}

func TestSnapshotRoutesForAdminAndIsDeniedForAgent(t *testing.T) {
	root := t.TempDir()
	repository, err := adapters.OpenJSONLRepository(filepath.Join(root, "accounts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	leases := accountapp.NewLeaseManager(time.Minute)
	accounts := accountapp.NewService(repository, leases, nil, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener, "admin-token", "agent-token", &managerStub{}, accounts, nil, leases)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve() }()
	defer server.Close(context.Background())

	admin := sessionruntime.NewHubClient("http://"+server.Endpoint(), "admin-token", nil)
	var snapshot sessionv1.SessionSnapshotResponse
	if err := admin.Invoke(ctx, sessionv1.MethodSessionSnapshot, sessionv1.SessionSnapshotRequest{
		SessionID: "session",
	}, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SessionID != "session" || snapshot.Watermark != 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}

	agent := sessionruntime.NewHubClient("http://"+server.Endpoint(), "agent-token", nil)
	err = agent.Invoke(ctx, sessionv1.MethodSessionSnapshot, sessionv1.SessionSnapshotRequest{
		SessionID: "session",
	}, &snapshot)
	coded, ok := err.(*sessionruntime.CodedError)
	if !ok || coded.Code != "permission_denied" {
		t.Fatalf("agent snapshot err=%T %v", err, err)
	}
}
