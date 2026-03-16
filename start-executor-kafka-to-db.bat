@echo off
setlocal

REM Start gruzilla-executor for Kafka -> DB sink scenarios

set GRUZILLA_DIR=%~dp0

cd /d %GRUZILLA_DIR%

echo Starting gruzilla-executor with scenario "scenarios\kafka-to-db-check.yml" on :8081 ...

REM Open executor in a separate console window so this script can exit
start "gruzilla-executor kafka-to-db-check" cmd /c "cd /d %GRUZILLA_DIR% && go run ./cmd/gruzilla-executor --scenario scenarios\kafka-to-db-check.yml --addr :8081"

echo.
echo Executor started (if Go and dependencies are installed).
echo Use run-kafka-db-sink-tests.bat to run Kafka -> DB tests.
pause

endlocal

