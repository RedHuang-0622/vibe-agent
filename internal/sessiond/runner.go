package sessiond

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"vibe-agent/internal/contextmgr"
	"vibe-agent/internal/conversation"
	"vibe-agent/internal/eventledger"
	"vibe-agent/internal/llm"
	"vibe-agent/internal/platform"
	"vibe-agent/internal/sessionruntime"
	"vibe-agent/internal/tooling"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
	"vibe-agent/tools/calculator"
	"vibe-agent/tools/memory"
	"vibe-agent/tools/readdocs"
	"vibe-agent/tools/search"
	"vibe-agent/tools/todo"
	"vibe-agent/tools/weather"
)

type Config struct {
	SessionID       string
	SessionDir      string
	Generation      uint64
	HubURL          string
	HubToken        string
	DocumentRoots   map[string]string
	MaxSteps        int
	MaxContextBytes int
}

func Run(ctx context.Context, config Config) error {
	if err := validateConfig(config); err != nil {
		return err
	}
	lock, err := platform.AcquireFileLock(ctx, filepath.Join(config.SessionDir, "session.lock"))
	if err != nil {
		return err
	}
	defer lock.Release()
	writer, err := eventledger.OpenWriter(
		filepath.Join(config.SessionDir, "messages.jsonl"),
		config.SessionID, eventledger.DefaultOptions(),
	)
	if err != nil {
		return err
	}
	defer writer.Close()

	hub := sessionruntime.NewHubClient(config.HubURL, config.HubToken, nil)
	accounts := &hubAccountClient{hub: hub}
	gateway := llm.NewGateway(accounts, accounts, nil)
	knowledge := &hubKnowledgeClient{hub: hub}
	registry, err := buildRegistry(config, gateway, knowledge)
	if err != nil {
		return err
	}
	tools := &registryExecutor{registry: registry, runtime: tooling.NewRuntime(registry, nil)}
	actorConfig := conversation.DefaultConfig(config.SessionID)
	if config.MaxSteps > 0 {
		actorConfig.MaxSteps = config.MaxSteps
	}
	if config.MaxContextBytes > 0 {
		actorConfig.MaxContextBytes = config.MaxContextBytes
	}
	actor, err := conversation.New(actorConfig, writer, writer, gateway, tools, contextmgr.BasicSummarizer{})
	if err != nil {
		return err
	}
	if err := actor.Start(ctx); err != nil {
		return err
	}
	defer actor.Close(context.Background())

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	token, err := sessionruntime.RandomToken(32)
	if err != nil {
		_ = listener.Close()
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	handler := &commandHandler{
		sessionID: config.SessionID, generation: config.Generation, actor: actor, writer: writer,
		tools: registry, shutdown: cancel,
		projectionDir: filepath.Join(config.SessionDir, "projections"),
	}
	server, err := sessionruntime.NewServer(listener, token, handler)
	if err != nil {
		return err
	}
	metadata := sessionruntime.Metadata{
		SessionID: config.SessionID, PID: os.Getpid(), Generation: config.Generation,
		Endpoint: server.Endpoint(), Token: token, Protocol: sessionruntimeProtocol(),
		StartedAt: time.Now().UTC(),
	}
	metadataPath := filepath.Join(config.SessionDir, "sessiond.json")
	if err := sessionruntime.WriteMetadata(metadataPath, metadata); err != nil {
		return err
	}
	defer os.Remove(metadataPath)
	return server.Serve(runCtx)
}

func buildRegistry(config Config, gateway *llm.Gateway, knowledge memory.Client) (*tooling.Registry, error) {
	registry := tooling.NewRegistry()
	if err := registry.Register(calculator.Spec(), calculator.New(), calculator.Adapter{}); err != nil {
		return nil, err
	}
	roots := config.DocumentRoots
	if len(roots) == 0 {
		workspace := filepath.Join(config.SessionDir, "workspace")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return nil, err
		}
		roots = map[string]string{"workspace": workspace}
	}
	readTool, err := readdocs.New(roots)
	if err != nil {
		return nil, err
	}
	todoStore, err := todo.OpenStore(filepath.Join(config.SessionDir, "todos.jsonl"))
	if err != nil {
		return nil, err
	}
	boardStore, err := todo.OpenBoardStore(filepath.Join(config.SessionDir, "todo-board.jsonl"))
	if err != nil {
		return nil, err
	}
	resolver := remoteResolver{gateway: gateway, capability: accountv1.CapabilitySearch}
	searchTool := search.New(resolver, nil)
	weatherTool := weather.New(resolver, nil)
	registrations := []struct {
		spec    toolv1.ToolSpec
		handler tooling.Handler
		direct  tooling.DirectAdapter
	}{
		{spec: readdocs.Spec(), handler: readTool, direct: readdocs.Adapter{}},
		{spec: todo.Spec(), handler: todo.New(todoStore), direct: todo.Adapter{}},
		{spec: todo.BoardSpec(), handler: todo.NewBoard(boardStore), direct: todo.BoardAdapter{}},
		{spec: search.Spec(), handler: searchTool, direct: search.Adapter{}},
		{spec: weather.Spec(), handler: weatherTool, direct: weather.Adapter{}},
		{spec: memory.Spec("write"), handler: memory.NewWrite(knowledge), direct: memory.WriteAdapter{}},
		{spec: memory.Spec("search"), handler: memory.NewSearch(knowledge), direct: memory.SearchAdapter{}},
		{spec: memory.Spec("update"), handler: memory.NewUpdate(knowledge), direct: memory.UpdateAdapter{}},
	}
	for _, registration := range registrations {
		if err := registry.Register(registration.spec, registration.handler, registration.direct); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

type registryExecutor struct {
	registry *tooling.Registry
	runtime  *tooling.Runtime
}

func (e *registryExecutor) Execute(ctx context.Context, call tooling.Call) tooling.Result {
	return e.runtime.Execute(ctx, call)
}

func (e *registryExecutor) ListTools() []toolv1.ToolSpec {
	return e.registry.List()
}

func validateConfig(config Config) error {
	if config.SessionID == "" || config.SessionDir == "" || config.HubURL == "" || config.HubToken == "" {
		return fmt.Errorf("sessiond configuration is incomplete")
	}
	return nil
}

func sessionruntimeProtocol() string {
	return sessionv1.ProtocolVersion
}
