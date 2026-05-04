@echo off
REM Rouse Relay - Windows Installer
REM
REM Sets up rouse-relay.exe to start automatically at boot using the
REM Windows Task Scheduler. No external dependencies (no NSSM required).
REM
REM Usage:
REM   1. Edit AUTH_TOKEN below
REM   2. Run this script as Administrator
REM   3. The relay starts now and on every boot until you uninstall it

set TASK_NAME=RouseRelay
set RELAY_DIR=%~dp0
set RELAY_EXE=%RELAY_DIR%rouse-relay.exe
set LAUNCHER=%RELAY_DIR%rouse-relay-launcher.bat
set AUTH_TOKEN=YOUR_PASSWORD_HERE
set PORT=9876

echo.
echo  Rouse Relay - Windows Installer
echo  ===============================
echo.

REM --- Sanity checks -------------------------------------------------------

REM Admin check
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo  ERROR: This script must be run as Administrator.
    echo  Right-click and select "Run as administrator".
    pause
    exit /b 1
)

REM Binary present
if not exist "%RELAY_EXE%" (
    echo  ERROR: rouse-relay.exe not found in the same folder as this script.
    echo  Expected: %RELAY_EXE%
    pause
    exit /b 1
)

REM Auth token edited
if "%AUTH_TOKEN%"=="YOUR_PASSWORD_HERE" (
    echo  ERROR: AUTH_TOKEN is still the placeholder value.
    echo  Open this script in a text editor, change AUTH_TOKEN at the top,
    echo  then run it again as Administrator.
    pause
    exit /b 1
)

REM --- Stop and remove any prior install -----------------------------------

schtasks /query /tn "%TASK_NAME%" >nul 2>&1
if %errorlevel% equ 0 (
    echo  Removing previous %TASK_NAME% scheduled task...
    schtasks /end /tn "%TASK_NAME%" >nul 2>&1
    schtasks /delete /tn "%TASK_NAME%" /f >nul 2>&1
)

REM --- Write the launcher --------------------------------------------------

REM A tiny wrapper batch file that exports the env vars and runs the relay.
REM Lives next to the binary so the Scheduled Task can call a single path.
echo  Writing launcher: %LAUNCHER%
(
    echo @echo off
    echo set AUTH_TOKEN=%AUTH_TOKEN%
    echo set PORT=%PORT%
    echo "%RELAY_EXE%"
) > "%LAUNCHER%"

REM --- Create the Scheduled Task ------------------------------------------

REM /sc onstart    - run when Windows boots
REM /ru SYSTEM     - run as LocalSystem so the user doesn't need to be logged in
REM /rl HIGHEST    - run with highest privileges (needed for some network ops)
REM /f             - overwrite if exists
echo  Creating scheduled task %TASK_NAME%...
schtasks /create ^
    /tn "%TASK_NAME%" ^
    /tr "\"%LAUNCHER%\"" ^
    /sc onstart ^
    /ru SYSTEM ^
    /rl HIGHEST ^
    /f
if %errorlevel% neq 0 (
    echo  ERROR: Failed to create scheduled task.
    pause
    exit /b 1
)

REM --- Start now ----------------------------------------------------------

echo  Starting %TASK_NAME%...
schtasks /run /tn "%TASK_NAME%"

echo.
echo  Done. Rouse Relay is running on port %PORT% and will auto-start on boot.
echo.
echo  To stop now:    schtasks /end /tn "%TASK_NAME%"
echo  To start now:   schtasks /run /tn "%TASK_NAME%"
echo  To uninstall:   schtasks /end /tn "%TASK_NAME%" ^&^& schtasks /delete /tn "%TASK_NAME%" /f
echo                  del "%LAUNCHER%"
echo.
pause
