@echo off
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-energy-preview.ps1" -EnableLan %*
endlocal
