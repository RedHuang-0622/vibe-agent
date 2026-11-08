package bootstrap

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vibe-agent/internal/accounts/adapters"
	accountapp "vibe-agent/internal/accounts/app"
	"vibe-agent/internal/hub"
	"vibe-agent/internal/knowledge"
	"vibe-agent/internal/llm"
	"vibe-agent/internal/platform"
	"vibe-agent/internal/sessionruntime"
)

func RunHub(ctx context.Context) error {
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.HubDir, 0o700); err != nil {
		return err
	}
	lock, err := platform.AcquireFileLock(ctx, filepath.Join(paths.HubDir, "hub.lock"))
	if err != nil {
		return err
	}
	defer lock.Release()
	state, err := loadOrCreateState(paths.HubState)
	if err != nil {
		return err
	}
	address := HubAddress()
	if !isLoopbackAddress(address) {
		return errors.New("hub address must bind to loopback")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	state.Endpoint = "http://" + listener.Addr().String()
	if err := SaveState(paths.HubState, state); err != nil {
		_ = listener.Close()
		return err
	}
	repository, err := adapters.OpenJSONLRepository(paths.AccountStore)
	if err != nil {
		_ = listener.Close()
		return err
	}
	leaseManager := accountapp.NewLeaseManager(2 * time.Minute)
	accountService := accountapp.NewService(
		repository, leaseManager, adapters.NewHTTPDemoSmokeRunner(nil),
		adapters.NewFileAudit(paths.AccountAudit),
	)
	gateway := llm.NewGateway(accountService, leaseManager, nil)
	knowledgeService, err := knowledge.Open(paths.MemoryDir, gateway, gateway, knowledge.DefaultConfig())
	if err != nil {
		_ = listener.Close()
		return err
	}
	manager, err := hub.NewManager(
		paths.DataRoot, paths.SessiondPath, state.Endpoint, state.AgentToken,
	)
	if err != nil {
		_ = listener.Close()
		return err
	}
	server, err := hub.NewServer(
		listener, state.AdminToken, state.AgentToken,
		manager, accountService, knowledgeService, leaseManager,
	)
	if err != nil {
		_ = listener.Close()
		return err
	}
	return server.Serve()
}

func HubClient() (*sessionruntime.HubClient, HubState, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return nil, HubState{}, err
	}
	state, err := LoadState(paths.HubState)
	if err != nil {
		return nil, HubState{}, err
	}
	return sessionruntime.NewHubClient(state.Endpoint, state.AdminToken, nil), state, nil
}

func hubClientFromState(state HubState) *sessionruntime.HubClient {
	return sessionruntime.NewHubClient(state.Endpoint, state.AdminToken, nil)
}

func loadOrCreateState(path string) (HubState, error) {
	state, err := LoadState(path)
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return HubState{}, err
	}
	adminToken, err := sessionruntime.RandomToken(32)
	if err != nil {
		return HubState{}, err
	}
	agentToken, err := sessionruntime.RandomToken(32)
	if err != nil {
		return HubState{}, err
	}
	return HubState{AdminToken: adminToken, AgentToken: agentToken}, nil
}

func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}
