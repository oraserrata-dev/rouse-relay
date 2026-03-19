@echo off
REM Rouse Relay — Windows Service Installer
REM Requires NSSM (https://nssm.cc) to be installed and on PATH
REM
REM Usage:
REM   1. Edit AUTH_TOKEN below
REM   2. Run this script as Administrator
REM   3. The relay will start automatically and survive reboots

set SERVICE_NAME=RouseRelay
set RELAY_EXE=%~dp0rouse-relay.exe
set AUTH_TOKEN=YOUR_PASSWORD_HERE
set PORT=9876

echo.
echo  Rouse Relay — Windows Service Installer
echo  =========================================
echo.

REM Check for admin privileges
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo  ERROR: This script must be run as Administrator.
    echo  Right-click and select "Run as administrator".
    pause
    exit /b 1
)

REM Check that the relay binary exists
if not exist "%RELAY_EXE%" (
    echo  ERROR: rouse-relay.exe not found in the same folder as this script.
    echo  Expected: %RELAY_EXE%
    pause
    exit /b 1
)

REM Check for NSSM
where nssm >nul 2>&1
if %errorlevel% neq 0 (
    echo  ERROR: NSSM not found on PATH.
    echo  Download from https://nssm.cc and add to PATH, or place nssm.exe
    echo  in the same folder as this script.
    pause
    exit /b 1
)

echo  Installing %SERVICE_NAME%...
nssm install %SERVICE_NAME% "%RELAY_EXE%"
nssm set %SERVICE_NAME% AppEnvironmentExtra AUTH_TOKEN=%AUTH_TOKEN% PORT=%PORT%
nssm set %SERVICE_NAME% DisplayName "Rouse Relay"
nssm set %SERVICE_NAME% Description "Wake-on-LAN relay server for Rouse"
nssm set %SERVICE_NAME% Start SERVICE_AUTO_START
nssm set %SERVICE_NAME% AppStdout %~dp0rouse-relay.log
nssm set %SERVICE_NAME% AppStderr %~dp0rouse-relay.log
nssm set %SERVICE_NAME% AppRotateFiles 1
nssm set %SERVICE_NAME% AppRotateBytes 1048576

echo  Starting %SERVICE_NAME%...
nssm start %SERVICE_NAME%

echo.
echo  Done! Rouse Relay is running on port %PORT%.
echo  Logs: %~dp0rouse-relay.log
echo.
echo  To stop:     nssm stop %SERVICE_NAME%
echo  To remove:   nssm remove %SERVICE_NAME% confirm
echo.
pause
