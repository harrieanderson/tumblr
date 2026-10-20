@echo off
cd /d "%~dp0"
echo DM auto-reply. Prints what they said + the draft, then sends it.
echo Use messages-reply.bat -dry-run to print only, no send.
echo Optional: messages-reply.bat -account planbhyderabad
echo.
if exist tumblr.exe (
  tumblr.exe -messages-reply %*
) else if exist tumblr-new.exe (
  tumblr-new.exe -messages-reply %*
) else (
  where go >nul 2>&1
  if errorlevel 1 (
    echo Go is missing. Run setup.bat first.
    pause
    exit /b 1
  )
  go run . -messages-reply %*
)
pause
