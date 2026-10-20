@echo off
cd /d "%~dp0"
echo Username + banner + purple/black theme.
echo No extra flags: first 20 logged-in blogs.
echo One account:  blog-look.bat -account ACCOUNTID
echo Start later:  blog-look.bat -from ACCOUNTID
echo All logged-in: blog-look.bat -account all
echo.
where go >nul 2>&1
if errorlevel 1 (
  echo Go is missing. Run setup.bat first.
  pause
  exit /b 1
)
go run . -blog-look %*
pause
