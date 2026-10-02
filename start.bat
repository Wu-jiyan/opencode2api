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
    echo HINT: create %CONFIG% from config.example.json and replace the placeholder keys.
    pause
    exit /b 1
)

rem A missing Xray is not fatal: with vless.auto_download_xray enabled the
rem gateway fetches it in the background on first start.
if not exist "bin\xray\xray.exe" (
    echo [WARN] Xray not found locally; it will be downloaded automatically if vless.auto_download_xray is enabled.
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