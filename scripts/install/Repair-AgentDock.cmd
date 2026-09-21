@echo off
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0repair-windows-preview.ps1"
if errorlevel 1 echo Recovery did not complete. Keep this window and report the error.
pause
