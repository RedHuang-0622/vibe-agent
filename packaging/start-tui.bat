@echo off
setlocal
chcp 65001 >nul
cd /d "%~dp0"

set "VIBE_AGENT_HOME=%~dp0data"
set "VIBE_HUB_ADDR=127.0.0.1:0"
set "VIBE_HUB_PATH=%~dp0bin\hub.exe"
set "VIBE_SESSIOND_PATH=%~dp0bin\sessiond.exe"

if not exist "%VIBE_HUB_PATH%" (
  echo Missing bin\hub.exe
  pause
  exit /b 1
)
if not exist "%VIBE_SESSIOND_PATH%" (
  echo Missing bin\sessiond.exe
  pause
  exit /b 1
)
if not exist "%~dp0bin\tui.exe" (
  echo Missing bin\tui.exe
  pause
  exit /b 1
)

if not defined DEEPSEEK_API_KEY (
  set /p "DEEPSEEK_API_KEY=Enter DEEPSEEK_API_KEY (not saved): "
)
if not defined DEEPSEEK_API_KEY (
  echo DEEPSEEK_API_KEY is required.
  pause
  exit /b 2
)

"%VIBE_HUB_PATH%" --version >nul 2>&1
"%~dp0bin\startup.exe" run
if errorlevel 1 (
  echo Failed to start hub.
  pause
  exit /b 1
)

"%VIBE_HUB_PATH%" llm account show deepseek-smoke >nul 2>&1
if errorlevel 1 (
  echo Importing local account configuration...
  "%VIBE_HUB_PATH%" llm account add --file "%~dp0config\llm-accounts.local.jsonl"
  if errorlevel 1 (
    echo Failed to import local account configuration.
    pause
    exit /b 1
  )
)

"%~dp0bin\tui.exe"
set "TUI_EXIT_CODE=%ERRORLEVEL%"
if not "%TUI_EXIT_CODE%"=="0" (
  echo TUI exited with code %TUI_EXIT_CODE%.
  pause
)
exit /b %TUI_EXIT_CODE%
