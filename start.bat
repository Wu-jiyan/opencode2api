@echo off
setlocal
title opencode2api
cd /d "%~dp0"

set "BINARY=opencode2api.exe"
set "CONFIG=config.json"

if not exist "%BINARY%" (
    echo [ERROR] %BINARY% not found in "%CD%"
    pause
    exit /b 1
)

if not exist "%CONFIG%" (
    echo [ERROR] %CONFIG% not found in "%CD%"
    pause
    exit /b 1
)

if not exist "bin\xray\xray.exe" (
    echo [WARN] bin\xray\xray.exe not found - the vless proxy pool will not start
    echo.
)

echo Starting opencode2api ...
echo   working directory: %CD%
echo   config file:       %CONFIG%
echo   stop:              Ctrl+C
echo.

"%BINARY%" -config "%CONFIG%" %*

set "CODE=%ERRORLEVEL%"
echo.
echo opencode2api exited with code %CODE%
pause
