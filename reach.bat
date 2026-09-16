@echo off
cd /d "%~dp0"
set TUMBLR_FAST=
echo Human-pace reach for about 1.5 hours. Screen stays on so the laptop does not sleep.
echo No mouse. One Chrome at a time if a session is fully logged out.
echo Stale cookies are copied from the profile with no window.
echo If Tumblr logged the profile out, it signs in, saves a new cookie, and closes Chrome.
echo Close Cursor first. Leave this window open.
echo.
if exist tumblr.exe (
  tumblr.exe -follow -parallel 20 -for 90
) else if exist tumblr-new.exe (
  tumblr-new.exe -follow -parallel 20 -for 90
) else (
  where go >nul 2>&1
  if errorlevel 1 (
    echo Go is missing. Run setup.bat first.
    pause
    exit /b 1
  )
  go run . -follow -parallel 20 -for 90
)
pause
