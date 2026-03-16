@echo off
setlocal

REM Run Kafka -> DB sink integration scenarios using gruzilla executor + kafka-db-sink

REM Adjust paths if needed
set GRUZILLA_DIR=%~dp0
set KAFKA_SINK_DIR=%~dp0..\kafka-db-sink

REM Basic test parameters
set KAFKA_BROKER=localhost:9092
set KAFKA_TOPIC=test-topic
set DB_DSN=postgres://postgres:postgres@localhost:5432/loadtest?sslmode=disable

REM Simple request id and timestamp
set REQUEST_ID=req-%RANDOM%
set TIMESTAMP=2026-03-13T12:00:00Z

echo.
echo === Kafka -> DB check scenario ===
echo Using broker %KAFKA_BROKER%, topic %KAFKA_TOPIC%, db %DB_DSN%
echo RequestId=%REQUEST_ID% Timestamp=%TIMESTAMP%
echo.

cd /d %GRUZILLA_DIR%

REM Assumes gruzilla-executor is already running with:
REM   go run ./cmd/gruzilla-executor --scenario scenarios/kafka-to-db-check.yml --addr :8081
REM and kafka-db-sink infra is up (docker compose up) and sink consumer is running.

go run ./cmd/gruzilla-cli run start ^
  --executor-url http://localhost:8081 ^
  --percent 100 ^
  --base-tps 1 ^
  --ramp-up-seconds 0 ^
  --var kafkaBroker=%KAFKA_BROKER% ^
  --var kafkaTopic=%KAFKA_TOPIC% ^
  --var dbDsn=%DB_DSN% ^
  --var requestId=%REQUEST_ID% ^
  --var timestamp=%TIMESTAMP%

echo.
echo === Kafka -> DB template scenario ===
set REQUEST_ID2=req-%RANDOM%

go run ./cmd/gruzilla-cli run start ^
  --executor-url http://localhost:8081 ^
  --percent 100 ^
  --base-tps 1 ^
  --ramp-up-seconds 0 ^
  --var kafkaBroker=%KAFKA_BROKER% ^
  --var kafkaTopic=%KAFKA_TOPIC% ^
  --var dbDsn=%DB_DSN% ^
  --var requestId=%REQUEST_ID2% ^
  --var timestamp=%TIMESTAMP% ^
  --var amount=100 ^
  --var currency=RUB

echo.
echo Done. Check postgres kafka_messages table for inserted records.
pause

endlocal

