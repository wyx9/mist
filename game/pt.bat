@echo off
rem one-click proto generation: calls scripts/gen_proto.ps1 (no make / PATH needed)
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\gen_proto.ps1"
