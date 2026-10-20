@echo off
cd /d "%~dp0"
set TUMBLR_FAST=
echo Overnight reach — pool up to 80 (top 20 followers + next 60).
echo Parallel 10 concurrent; rotates through the pool each pass.
echo Soft gender/inactive skips no longer burn the shared interacted list.
echo No mouse. One Chrome at a time if a session is fully logged out.
echo Close Cursor first. Leave this window open.
echo.
set RUN=
where go >nul 2>&1
if not errorlevel 1 (
  set "RUN=go run ."
) else if exist tumblr.exe (
  set "RUN=tumblr.exe"
) else if exist tumblr-new.exe (
  set "RUN=tumblr-new.exe"
) else (
  echo Go is missing. Run setup.bat first.
  pause
  exit /b 1
)
%RUN% -follow -parallel 10 -for 8h
echo.
echo Reach window done — starting DM replies on top 10 follower blogs for 3h.
%RUN% -messages-reply -top-followers 10 -for 3h
pause
