package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func StartAutostartPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", errors.New("APPDATA is not configured")
		}
		return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "vibe-agent.cmd"), nil
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", "com.vibe-agent.startup.plist"), nil
	default:
		configDir := os.Getenv("XDG_CONFIG_HOME")
		if configDir == "" {
			configDir = filepath.Join(home, ".config")
		}
		return filepath.Join(configDir, "autostart", "vibe-agent.desktop"), nil
	}
}

func InstallAutostart(startupExecutable string) error {
	path, err := StartAutostartPath()
	if err != nil {
		return err
	}
	content := autostartContent(startupExecutable)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".autostart-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.WriteString(content); err != nil {
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

func UninstallAutostart() error {
	path, err := StartAutostartPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func AutostartStatus() (string, bool, error) {
	path, err := StartAutostartPath()
	if err != nil {
		return "", false, err
	}
	_, statErr := os.Stat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return path, false, nil
	}
	return path, statErr == nil, statErr
}

func autostartContent(executable string) string {
	executable = strings.ReplaceAll(executable, `"`, `\"`)
	switch runtime.GOOS {
	case "windows":
		return fmt.Sprintf("@echo off\r\nstart \"\" \"%s\" run\r\n", executable)
	case "darwin":
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.vibe-agent.startup</string>
<key>ProgramArguments</key><array><string>%s</string><string>run</string></array>
<key>RunAtLoad</key><true/>
</dict></plist>
`, executable)
	default:
		return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=vibe_agent
Exec="%s" run
X-GNOME-Autostart-enabled=true
`, executable)
	}
}
