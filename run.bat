@echo off
cd /d "%~dp0"

rem Native desktop window (tkinter). Falls back to the local web GUI if Python is missing.
where pyw >nul 2>&1
if not errorlevel 1 (
  py -3 -c "import tkinter" >nul 2>&1
  if not errorlevel 1 (
    start "Tumblr bot" pyw -3 "%~dp0gui.py"
    exit /b 0
  )
)
where pythonw >nul 2>&1
if not errorlevel 1 (
  python -c "import tkinter" >nul 2>&1
  if not errorlevel 1 (
    start "Tumblr bot" pythonw "%~dp0gui.py"
    exit /b 0
  )
)
where py >nul 2>&1
if not errorlevel 1 (
  py -3 -c "import tkinter" >nul 2>&1
  if not errorlevel 1 (
    start "Tumblr bot" py -3 "%~dp0gui.py"
    exit /b 0
  )
)
where python >nul 2>&1
if not errorlevel 1 (
  python -c "import tkinter" >nul 2>&1
  if not errorlevel 1 (
    start "Tumblr bot" python "%~dp0gui.py"
    exit /b 0
  )
)

echo Python/tkinter not found — opening the local web GUI instead.
echo Install Python from python.org (tick tcl/tk) for a desktop window.
if exist tumblr-new.exe (
  tumblr-new.exe -gui
) else if exist tumblr.exe (
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
