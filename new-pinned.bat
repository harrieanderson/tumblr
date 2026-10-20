@echo off
cd /d "%~dp0"
echo Posting+pinning new_pinned.mp4 (Clicky/beacons caption) on fully-setup blogs...
go run . -new-pinned -fully-setup
echo.
pause
