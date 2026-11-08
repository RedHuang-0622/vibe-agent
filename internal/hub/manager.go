package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"vibe-agent/internal/sessionruntime"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

type Manager struct {
	mu           sync.Mutex
	dataRoot     string
	sessiondPath string
	hubEndpoint  string
	agentToken   string
	sessions     map[string]*managedSession
}

type managedSession struct {
	sessionv1.SessionView
	dir       string
	client    *sessionruntime.Client
	process   *os.Process
	done      <-chan struct{}
	restartMu sync.Mutex
}

type sessionFile struct {
	SessionID  string    `json:"session_id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	Generation uint64    `json:"generation"`
}

func NewManager(dataRoot, sessiondPath, hubEndpoint, agentToken string) (*Manager, error) {
	if dataRoot == "" || sessiondPath == "" || hubEndpoint == "" || agentToken == "" {
		return nil, errors.New("manager configuration is incomplete")
	}
	manager := &Manager{
		dataRoot: dataRoot, sessiondPath: sessiondPath,
		hubEndpoint: hubEndpoint, agentToken: agentToken,
		sessions: make(map[string]*managedSession),
	}
	if err := manager.Reconcile(context.Background()); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Create(ctx context.Context, request sessionv1.CreateSessionRequest) (sessionv1.SessionView, error) {
	id := "sess-" + randomHex(8)
	name := request.Name
	if name == "" {
		name = id
	}
	dir := filepath.Join(m.dataRoot, "sessions", id)
	if err := os.MkdirAll(filepath.Join(dir, "projections"), 0o700); err != nil {
		return sessionv1.SessionView{}, err
	}
	state := sessionFile{SessionID: id, Name: name, CreatedAt: time.Now().UTC(), Generation: 1}
	if err := writeJSONAtomic(filepath.Join(dir, "session.json"), state); err != nil {
		return sessionv1.SessionView{}, err
	}
	view, done, err := m.startLocked(ctx, state, dir)
	if err != nil {
		return sessionv1.SessionView{}, err
	}
	process, _ := os.FindProcess(view.PID)
	metadata, err := sessionruntime.ReadMetadata(filepath.Join(dir, "sessiond.json"))
	if err != nil {
		_ = process.Kill()
		return sessionv1.SessionView{}, err
	}
	m.mu.Lock()
	m.sessions[id] = &managedSession{
		SessionView: view, dir: dir, process: process,
		client: sessionruntime.NewClient(metadata.Endpoint, metadata.Token), done: done,
	}
	m.mu.Unlock()
	return view, nil
}

func (m *Manager) List(context.Context) ([]sessionv1.SessionView, error) {
	m.mu.Lock()
	sessionsCopy := make([]*managedSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessionsCopy = append(sessionsCopy, session)
	}
	m.mu.Unlock()
	views := make([]sessionv1.SessionView, 0, len(sessionsCopy))
	for _, session := range sessionsCopy {
		session.restartMu.Lock()
		if session.client != nil {
			healthCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			health, err := session.client.Health(healthCtx)
			cancel()
			if err == nil && health.SessionID == session.SessionID && health.Generation == session.Generation {
				session.State = health.State
				session.LastSeq = health.LastSeq
				session.Error = ""
			} else {
				session.State = sessionv1.StateFailed
				if err != nil {
					session.Error = err.Error()
				} else {
					session.Error = "sessiond health identity mismatch"
				}
			}
		}
		views = append(views, session.SessionView)
		session.restartMu.Unlock()
	}
	sort.Slice(views, func(i, j int) bool { return views[i].SessionID < views[j].SessionID })
	return views, nil
}

func (m *Manager) Inspect(ctx context.Context, id string) (sessionv1.SessionView, error) {
	session, err := m.session(id)
	if err != nil {
		return sessionv1.SessionView{}, err
	}
	session.restartMu.Lock()
	defer session.restartMu.Unlock()
	if session.client != nil {
		health, healthErr := session.client.Health(ctx)
		if healthErr == nil && health.SessionID == session.SessionID && health.Generation == session.Generation {
			session.State = health.State
			session.LastSeq = health.LastSeq
			session.Error = ""
		} else {
			session.State = sessionv1.StateFailed
			if healthErr != nil {
				session.Error = healthErr.Error()
			} else {
				session.Error = "sessiond health identity mismatch"
			}
		}
	}
	return session.SessionView, nil
}

func (m *Manager) Route(ctx context.Context, id, method string, payload, target any) error {
	session, err := m.session(id)
	if err != nil {
		return err
	}
	client, err := m.ensureRunning(ctx, session)
	if err != nil {
		return err
	}
	return client.Do(ctx, method, payload, target)
}

func (m *Manager) ensureRunning(ctx context.Context, session *managedSession) (*sessionruntime.Client, error) {
	session.restartMu.Lock()
	defer session.restartMu.Unlock()
	if session.State == sessionv1.StateStopped {
		return nil, &sessionruntime.CodedError{Code: "not_found", Message: "session is stopped"}
	}
	if session.client != nil {
		healthCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := session.client.Health(healthCtx)
		cancel()
		if err == nil {
			return session.client, nil
		}
	}
	state, err := readSessionFile(filepath.Join(session.dir, "session.json"))
	if err != nil {
		return nil, err
	}
	state.Generation++
	if err := writeJSONAtomic(filepath.Join(session.dir, "session.json"), state); err != nil {
		return nil, err
	}
	view, done, err := m.startLocked(ctx, state, session.dir)
	if err != nil {
		session.State = sessionv1.StateFailed
		session.Error = err.Error()
		return nil, err
	}
	metadata, err := sessionruntime.ReadMetadata(filepath.Join(session.dir, "sessiond.json"))
	if err != nil {
		return nil, err
	}
	process, _ := os.FindProcess(view.PID)
	session.SessionView = view
	session.client = sessionruntime.NewClient(metadata.Endpoint, metadata.Token)
	session.process = process
	session.done = done
	return session.client, nil
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	session, err := m.session(id)
	if err != nil {
		return err
	}
	session.restartMu.Lock()
	defer session.restartMu.Unlock()
	if session.client != nil {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = session.client.Do(stopCtx, sessionv1.MethodSessionShutdown, struct{}{}, nil)
		cancel()
	}
	exited := waitForProcessExit(ctx, session.done, session.client)
	if !exited && session.process != nil {
		_ = session.process.Kill()
	}
	session.State = sessionv1.StateStopped
	session.client = nil
	session.process = nil
	session.done = nil
	return nil
}

func (m *Manager) Reconcile(ctx context.Context) error {
	root := filepath.Join(m.dataRoot, "sessions")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		data, readErr := os.ReadFile(filepath.Join(dir, "session.json"))
		if readErr != nil {
			continue
		}
		var state sessionFile
		if json.Unmarshal(data, &state) != nil {
			continue
		}
		metadata, metadataErr := sessionruntime.ReadMetadata(filepath.Join(dir, "sessiond.json"))
		if metadataErr != nil || !metadataMatches(state, metadata) {
			m.sessions[state.SessionID] = &managedSession{
				SessionView: sessionv1.SessionView{
					SessionID: state.SessionID, Name: state.Name,
					State: sessionv1.StateStopped, Generation: state.Generation,
				},
				dir: dir,
			}
			continue
		}
		client := sessionruntime.NewClient(metadata.Endpoint, metadata.Token)
		healthCtx, cancel := context.WithTimeout(ctx, time.Second)
		health, healthErr := client.Health(healthCtx)
		cancel()
		if healthErr != nil {
			m.sessions[state.SessionID] = &managedSession{
				SessionView: sessionv1.SessionView{
					SessionID: state.SessionID, Name: state.Name,
					State: sessionv1.StateFailed, Generation: metadata.Generation,
				},
				dir: dir, client: client,
			}
			continue
		}
		if !healthMatches(state.SessionID, metadata, health) {
			m.sessions[state.SessionID] = &managedSession{
				SessionView: sessionv1.SessionView{
					SessionID: state.SessionID, Name: state.Name,
					State: sessionv1.StateFailed, Generation: state.Generation,
				},
				dir: dir,
			}
			continue
		}
		process, _ := os.FindProcess(metadata.PID)
		m.sessions[state.SessionID] = &managedSession{
			SessionView: sessionv1.SessionView{
				SessionID: state.SessionID, Name: state.Name, State: health.State,
				PID: metadata.PID, Generation: metadata.Generation, LastSeq: health.LastSeq,
			},
			dir: dir, client: client, process: process,
		}
	}
	return nil
}

func (m *Manager) startLocked(ctx context.Context, state sessionFile, dir string) (sessionv1.SessionView, <-chan struct{}, error) {
	if m.sessiondPath == "" {
		return sessionv1.SessionView{}, nil, errors.New("sessiond executable path is required")
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "sessiond.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return sessionv1.SessionView{}, nil, err
	}
	defer logFile.Close()
	command := exec.Command(
		m.sessiondPath,
		"--session-id", state.SessionID,
		"--session-dir", dir,
		"--generation", fmt.Sprint(state.Generation),
		"--hub-url", m.hubEndpoint,
	)
	command.Env = append(os.Environ(), "VIBE_HUB_TOKEN="+m.agentToken)
	command.Stdout = logFile
	command.Stderr = logFile
	configureCommand(command)
	if err := command.Start(); err != nil {
		return sessionv1.SessionView{}, nil, err
	}
	process := command.Process
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	if err := m.waitReady(ctx, dir, process.Pid); err != nil {
		_ = process.Kill()
		return sessionv1.SessionView{}, nil, err
	}
	metadata, err := sessionruntime.ReadMetadata(filepath.Join(dir, "sessiond.json"))
	if err != nil {
		_ = process.Kill()
		return sessionv1.SessionView{}, nil, err
	}
	if !metadataMatches(state, metadata) {
		_ = process.Kill()
		return sessionv1.SessionView{}, nil, errors.New("sessiond metadata identity mismatch")
	}
	client := sessionruntime.NewClient(metadata.Endpoint, metadata.Token)
	health, err := client.Health(ctx)
	if err != nil {
		_ = process.Kill()
		return sessionv1.SessionView{}, nil, err
	}
	if !healthMatches(state.SessionID, metadata, health) {
		_ = process.Kill()
		return sessionv1.SessionView{}, nil, errors.New("sessiond health identity mismatch")
	}
	return sessionv1.SessionView{
		SessionID: state.SessionID, Name: state.Name, State: health.State,
		PID: process.Pid, Generation: metadata.Generation, LastSeq: health.LastSeq,
	}, done, nil
}

func (m *Manager) waitReady(ctx context.Context, dir string, pid int) error {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		metadata, err := sessionruntime.ReadMetadata(filepath.Join(dir, "sessiond.json"))
		if err == nil && metadata.PID == pid {
			client := sessionruntime.NewClient(metadata.Endpoint, metadata.Token)
			healthCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			_, healthErr := client.Health(healthCtx)
			cancel()
			if healthErr == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return errors.New("sessiond did not become ready")
}

func metadataMatches(state sessionFile, metadata sessionruntime.Metadata) bool {
	return metadata.SessionID == state.SessionID &&
		metadata.Generation == state.Generation &&
		metadata.PID > 0 &&
		metadata.Endpoint != "" &&
		metadata.Token != "" &&
		metadata.Protocol == sessionv1.ProtocolVersion
}

func healthMatches(sessionID string, metadata sessionruntime.Metadata, health sessionv1.HealthResponse) bool {
	return health.SessionID == sessionID &&
		health.PID == metadata.PID &&
		health.Generation == metadata.Generation &&
		health.Protocol == sessionv1.ProtocolVersion
}

func waitForProcessExit(ctx context.Context, done <-chan struct{}, client *sessionruntime.Client) bool {
	if done == nil && client == nil {
		return false
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if done != nil {
		select {
		case <-done:
			return true
		case <-waitCtx.Done():
			return false
		}
	}
	for {
		healthCtx, healthCancel := context.WithTimeout(waitCtx, 200*time.Millisecond)
		_, err := client.Health(healthCtx)
		healthCancel()
		if err != nil {
			return true
		}
		select {
		case <-waitCtx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (m *Manager) session(id string) (*managedSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, &sessionruntime.CodedError{Code: "not_found", Message: "session not found"}
	}
	return session, nil
}

func randomHex(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(value)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".session-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func readSessionFile(path string) (sessionFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionFile{}, err
	}
	var state sessionFile
	if err := json.Unmarshal(data, &state); err != nil {
		return sessionFile{}, err
	}
	return state, nil
}
