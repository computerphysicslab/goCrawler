@echo off
REM Run goCrawler from any directory by resolving the Go module root first.
SETLOCAL
cd /d "%~dp0"
go run crawler.go %*
EXIT /B %ERRORLEVEL%
