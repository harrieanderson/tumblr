@echo off
cd /d "%~dp0"
if exist tumblr.exe (
  tumblr.exe -gui
) else (
  where go >nul 2>&1
  if errorlevel 1 (
    echo Go is missing. Run setup.bat first.
    pause
    exit /b 1
  )
  go run . -gui
)
pause
