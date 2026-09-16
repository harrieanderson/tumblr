@echo off
cd /d "%~dp0"
echo Setting avatar, name, bio, birthday, and AgeKey on each account.
echo After AgeKey finishes in Chrome, press Enter in THIS window for the next account.
echo Face-scan-only flow is still: age-verify.bat
echo Optional: setup-profile.bat -from 9
echo.
if exist tumblr.exe (
  tumblr.exe -setup-profile %*
) else if exist tumblr-new.exe (
  tumblr-new.exe -setup-profile %*
) else (
  where go >nul 2>&1
  if errorlevel 1 (
    echo Go is missing. Run setup.bat first.
    pause
    exit /b 1
  )
  go run . -setup-profile %*
)
pause
pause
