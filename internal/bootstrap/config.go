package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const defaultHubAddress = "127.0.0.1:47831"

type HubState struct {
	Endpoint   string `json:"endpoint"`
	AdminToken string `json:"admin_token"`
	AgentToken string `json:"agent_token"`
}

type Paths struct {
	DataRoot     string
	HubDir       string
	HubState     string
	AccountStore string
	AccountAudit string
	MemoryDir    string
	SessiondPath string
}

func ResolvePaths() (Paths, error) {
	root := os.Getenv("VIBE_AGENT_HOME")
	if root == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return Paths{}, err
		}
		root = filepath.Join(configDir, "vibe_agent")
	}
	hubDir := filepath.Join(root, "hub")
	sessiondPath, err := findSessiond()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		DataRoot: root, HubDir: hubDir,
		HubState:     filepath.Join(hubDir, "state.json"),
		AccountStore: filepath.Join(root, "accounts", "accounts.jsonl"),
		AccountAudit: filepath.Join(root, "accounts", "accounts-audit.jsonl"),
		MemoryDir:    filepath.Join(root, "memory"),
		SessiondPath: sessiondPath,
	}, nil
}

func LoadState(path string) (HubState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return HubState{}, err
	}
	var state HubState
	if err := json.Unmarshal(data, &state); err != nil {
		return HubState{}, err
	}
	if state.AdminToken == "" || state.AgentToken == "" {
		return HubState{}, errors.New("hub state is missing tokens")
	}
	return state, nil
}

func SaveState(path string, state HubState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
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

func HubAddress() string {
	if value := os.Getenv("VIBE_HUB_ADDR"); value != "" {
		return value
	}
	return defaultHubAddress
}

func findSessiond() (string, error) {
	if value := os.Getenv("VIBE_SESSIOND_PATH"); value != "" {
		return value, nil
	}
	name := "sessiond"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable, err := os.Executable()
	if err == nil {
		candidates := []string{
			filepath.Join(filepath.Dir(executable), name),
			filepath.Join(filepath.Dir(executable), "bin", name),
			filepath.Join("bin", name),
		}
		for _, candidate := range candidates {
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				absolute, _ := filepath.Abs(candidate)
				return absolute, nil
			}
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", errors.New("sessiond executable not found; set VIBE_SESSIOND_PATH or build bin/sessiond")
}

func findHub() (string, error) {
	if value := os.Getenv("VIBE_HUB_PATH"); value != "" {
		return value, nil
	}
	name := "hub"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable, err := os.Executable()
	if err == nil {
		candidates := []string{
			filepath.Join(filepath.Dir(executable), name),
			filepath.Join(filepath.Dir(executable), "bin", name),
			filepath.Join("bin", name),
		}
		for _, candidate := range candidates {
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				absolute, _ := filepath.Abs(candidate)
				return absolute, nil
			}
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", errors.New("hub executable not found; set VIBE_HUB_PATH or build bin/hub")
}

func HubExecutablePath() (string, error) {
	return findHub()
}
