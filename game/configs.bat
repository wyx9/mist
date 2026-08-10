@echo off
rem one-click config generation: calls scripts/gen_configs.ps1 (no make / PATH needed)
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\gen_configs.ps1" %*
