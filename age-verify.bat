@echo off
cd /d "%~dp0"
echo Logging into each account, then opening Settings for age verification.
echo After you finish the face scan, press Enter in THIS window for the next account.
echo.
if exist tumblr-new.exe (
  tumblr-new.exe -safe-mode-off
) else if exist tumblr.exe (
  tumblr.exe -safe-mode-off
) else (
  where go >nul 2>&1
  if errorlevel 1 (
    echo Go is missing. Run setup.bat first.
    pause
    exit /b 1
  )
  go run . -safe-mode-off
)
pause
