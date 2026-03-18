import {
  Button,
  Card,
  CardContent,
  Chip,
  Grid,
  MenuItem,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from "@mui/material";
import { useEffect, useMemo, useRef, useState } from "react";
import { extractCliData, postApi } from "../api/client";
import ResponseCard from "./ResponseCard";

function normalizeAddr(addr) {
  const v = String(addr || "").trim();
  if (!v) return "";
  if (v.startsWith(":")) return `http://localhost${v}`;
  if (v.startsWith("http://") || v.startsWith("https://")) return v;
  return `http://localhost:${v}`;
}

function parseExecutors(data) {
  if (Array.isArray(data)) {
    return data.map((item) => ({
      pid: item?.PID ?? item?.pid ?? 0,
      scenario: item?.Scenario ?? item?.scenario ?? "",
      addr: item?.Addr ?? item?.addr ?? "",
      status: "unknown",
      attemptsCount: null,
      successCount: null,
      errorCount: null,
      currentTps: null,
      percent: null,
      baseTps: null,
      rampUpSeconds: null,
    }));
  }
  const lines = Array.isArray(data?.lines) ? data.lines : [];
  return lines
    .map((line) => {
      const text = String(line || "");
      const pid = Number((text.match(/pid=(\d+)/) || [])[1] || 0);
      const addr = (text.match(/addr=([^\s]+)/) || [])[1] || "";
      const scenario = (text.match(/scenario=([^\s]+)/) || [])[1] || "";
      if (!pid) return null;
      return {
        pid,
        addr,
        scenario,
        status: "unknown",
        attemptsCount: null,
        successCount: null,
        errorCount: null,
        currentTps: null,
        percent: null,
        baseTps: null,
        rampUpSeconds: null,
      };
    })
    .filter(Boolean);
}

function statusColor(status) {
  switch (status) {
    case "running":
      return "success";
    case "stopped":
      return "default";
    case "unreachable":
      return "error";
    default:
      return "warning";
  }
}

export default function ExecutorsPanel({ baseUrl, onExecutorSelected, statsRefreshSeconds = 5 }) {
  const [scenario, setScenario] = useState("");
  const [scenarioDir, setScenarioDir] = useState("scenarios");
  const [scenarios, setScenarios] = useState([]);
  const [addr, setAddr] = useState(":8081");
  const [bin, setBin] = useState("go");
  const [rows, setRows] = useState([]);
  const [selectedExecutor, setSelectedExecutor] = useState(null);
  const [percent, setPercent] = useState(100);
  const [baseTps, setBaseTps] = useState(100);
  const [rampUp, setRampUp] = useState(0);
  const [lastResponse, setLastResponse] = useState(null);
  const [loading, setLoading] = useState(false);
  const autoRefreshingRef = useRef(false);

  const count = useMemo(() => rows.length, [rows.length]);

  const syncControlsFromRow = (row) => {
    if (!row) return;
    if (Number.isFinite(Number(row.percent))) {
      setPercent(Number(row.percent));
    }
    if (Number.isFinite(Number(row.baseTps))) {
      setBaseTps(Number(row.baseTps));
    }
    if (Number.isFinite(Number(row.rampUpSeconds))) {
      setRampUp(Number(row.rampUpSeconds));
    }
  };

  const loadStatsForRows = async (baseRows) => {
    if (baseRows.length === 0) return baseRows;
    const updates = await Promise.all(
      baseRows.map(async (row) => {
        const executorUrl = normalizeAddr(row.addr);
        const response = await postApi(
          "/api/v1/run/status",
          { executor_url: executorUrl },
          { baseUrl },
        );

        const data = extractCliData(response.payload);
        if (!data || typeof data !== "object") {
          return {
            pid: row.pid,
            status: "unreachable",
            attemptsCount: null,
            successCount: null,
            errorCount: null,
            currentTps: null,
          };
        }

        const running = data.running === true;
        const metrics = data?.metrics ?? {};
        const config = data?.config ?? {};
        const attemptsCount = metrics?.attempts_count;
        const successCount = metrics?.success_count;
        const errorCount = metrics?.error_count;
        const currentTps = metrics?.current_tps;
        const percent = config?.percent;
        const baseTps = config?.base_tps;
        const rampUpSeconds = config?.ramp_up_seconds;
        return {
          pid: row.pid,
          status: running ? "running" : "stopped",
          attemptsCount: Number.isFinite(Number(attemptsCount)) ? Number(attemptsCount) : null,
          successCount: Number.isFinite(Number(successCount)) ? Number(successCount) : null,
          errorCount: Number.isFinite(Number(errorCount)) ? Number(errorCount) : null,
          currentTps: Number.isFinite(Number(currentTps)) ? Number(currentTps).toFixed(2) : null,
          percent: Number.isFinite(Number(percent)) ? Number(percent) : null,
          baseTps: Number.isFinite(Number(baseTps)) ? Number(baseTps) : null,
          rampUpSeconds: Number.isFinite(Number(rampUpSeconds)) ? Number(rampUpSeconds) : null,
        };
      }),
    );
    const byPID = new Map(updates.map((u) => [u.pid, u]));
    return baseRows.map((row) => {
      const patch = byPID.get(row.pid);
      if (!patch) return row;
      return {
        ...row,
        status: patch.status,
        attemptsCount: patch.attemptsCount,
        successCount: patch.successCount,
        errorCount: patch.errorCount,
        currentTps: patch.currentTps,
        percent: patch.percent,
        baseTps: patch.baseTps,
        rampUpSeconds: patch.rampUpSeconds,
      };
    });
  };

  const refresh = async ({ silent = false } = {}) => {
    if (silent) {
      if (autoRefreshingRef.current) return;
      autoRefreshingRef.current = true;
    } else {
      setLoading(true);
    }
    try {
      const response = await postApi("/api/v1/executors/list", {}, { baseUrl });
      const data = extractCliData(response.payload);
      const baseRows = parseExecutors(data);
      const enriched = await loadStatsForRows(baseRows);
      setRows(enriched);
      if (selectedExecutor?.pid) {
        const current = enriched.find((r) => r.pid === selectedExecutor.pid);
        if (current) {
          setSelectedExecutor({ ...current, url: normalizeAddr(current.addr) });
          syncControlsFromRow(current);
        }
      }
    } finally {
      if (silent) {
        autoRefreshingRef.current = false;
      } else {
        setLoading(false);
      }
    }
  };

  const loadScenarios = async () => {
    const response = await postApi("/api/v1/scenarios/list", { dir: scenarioDir }, { baseUrl });
    const data = extractCliData(response.payload);
    const lines = Array.isArray(data?.lines) ? data.lines : [];
    setScenarios(lines);
    if (!scenario && lines.length > 0) {
      setScenario(lines[0]);
    }
  };

  const startExecutor = async () => {
    setLoading(true);
    try {
      const response = await postApi(
        "/api/v1/executors/start",
        { scenario, addr, bin },
        { baseUrl },
      );
      setLastResponse(response);
      await refresh();
      const normalized = normalizeAddr(addr);
      const selected = { addr, url: normalized, scenario };
      setSelectedExecutor(selected);
      onExecutorSelected?.(normalized);
      syncControlsFromRow(selected);
    } finally {
      setLoading(false);
    }
  };

  const stopExecutor = async (pid, rowAddr) => {
    setLoading(true);
    try {
      const response = await postApi(
        "/api/v1/executors/stop",
        { pid, addr: rowAddr },
        { baseUrl },
      );
      setLastResponse(response);
      await refresh();
      if (selectedExecutor?.addr === rowAddr) {
        setSelectedExecutor(null);
      }
    } finally {
      setLoading(false);
    }
  };

  const runAction = async (path, body = {}) => {
    if (!selectedExecutor?.url) return;
    setLoading(true);
    try {
      const response = await postApi(
        path,
        { executor_url: selectedExecutor.url, ...body },
        { baseUrl },
      );
      setLastResponse(response);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh();
    loadScenarios();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [baseUrl]);

  useEffect(() => {
    const sec = Number(statsRefreshSeconds);
    if (!Number.isFinite(sec) || sec <= 0) {
      return undefined;
    }
    const timer = setInterval(() => {
      refresh({ silent: true });
    }, sec * 1000);
    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [statsRefreshSeconds, baseUrl]);

  return (
    <Stack spacing={2}>
      <Card>
        <CardContent>
          <Stack spacing={2}>
            <Stack direction="row" spacing={1} alignItems="center">
              <Typography variant="h6">Executors</Typography>
              <Chip size="small" label={count} />
              <Button size="small" variant="outlined" onClick={refresh} disabled={loading}>
                Обновить
              </Button>
            </Stack>

            <Grid container spacing={2}>
              <Grid size={{ xs: 12, md: 3 }}>
                <TextField
                  label="Scenario Directory"
                  value={scenarioDir}
                  onChange={(e) => setScenarioDir(e.target.value)}
                  fullWidth
                />
              </Grid>
              <Grid size={{ xs: 12, md: 5 }}>
                {scenarios.length > 0 ? (
                  <TextField
                    select
                    label="Config (scenario)"
                    value={scenario}
                    onChange={(e) => setScenario(e.target.value)}
                    fullWidth
                  >
                    {scenarios.map((item) => (
                      <MenuItem key={item} value={item}>
                        {item}
                      </MenuItem>
                    ))}
                  </TextField>
                ) : (
                  <TextField
                    label="Config (scenario path)"
                    value={scenario}
                    onChange={(e) => setScenario(e.target.value)}
                    fullWidth
                    helperText="Список пуст. Можно ввести путь вручную."
                  />
                )}
              </Grid>
              <Grid size={{ xs: 6, md: 2 }}>
                <TextField
                  label="Port / Addr"
                  value={addr}
                  onChange={(e) => setAddr(e.target.value)}
                  fullWidth
                  helperText="Напр. :8082"
                />
              </Grid>
              <Grid size={{ xs: 6, md: 2 }}>
                <TextField
                  label="Bin"
                  value={bin}
                  onChange={(e) => setBin(e.target.value)}
                  fullWidth
                />
              </Grid>
              <Grid size={{ xs: 12, md: 2 }}>
                <Stack spacing={1}>
                  <Button variant="contained" fullWidth disabled={loading || !scenario} onClick={startExecutor}>
                    Создать executor
                  </Button>
                </Stack>
              </Grid>
            </Grid>

            <TableContainer sx={{ width: "100%", overflowX: "auto" }}>
              <Table size="small" sx={{ minWidth: 1800 }}>
                <TableHead>
                  <TableRow>
                    <TableCell>PID</TableCell>
                    <TableCell>Config</TableCell>
                    <TableCell>Порт</TableCell>
                    <TableCell>Статус</TableCell>
                    <TableCell>Попытки</TableCell>
                    <TableCell>Успех</TableCell>
                    <TableCell>Ошибки</TableCell>
                    <TableCell>Current TPS</TableCell>
                    <TableCell align="right">Действия</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {rows.map((row) => (
                    <TableRow
                      key={row.pid}
                      hover
                      selected={selectedExecutor?.pid === row.pid}
                      onClick={() => {
                        const normalized = normalizeAddr(row.addr);
                        setSelectedExecutor({ ...row, url: normalized });
                        onExecutorSelected?.(normalized);
                        syncControlsFromRow(row);
                      }}
                      sx={{ cursor: "pointer" }}
                    >
                      <TableCell>{row.pid}</TableCell>
                      <TableCell>{row.scenario}</TableCell>
                      <TableCell>{row.addr}</TableCell>
                      <TableCell>
                        <Chip size="small" label={row.status} color={statusColor(row.status)} />
                      </TableCell>
                      <TableCell>{row.attemptsCount ?? "-"}</TableCell>
                      <TableCell>{row.successCount ?? "-"}</TableCell>
                      <TableCell>{row.errorCount ?? "-"}</TableCell>
                      <TableCell>{row.currentTps ?? "-"}</TableCell>
                      <TableCell align="right">
                        <Stack direction="row" spacing={1} justifyContent="flex-end">
                          <Button
                            size="small"
                            color="error"
                            variant="outlined"
                            disabled={loading}
                            onClick={(e) => {
                              e.stopPropagation();
                              stopExecutor(row.pid, row.addr);
                            }}
                          >
                            Остановить
                          </Button>
                        </Stack>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>

            {selectedExecutor && (
              <Card variant="outlined">
                <CardContent>
                  <Stack spacing={2}>
                    <Typography variant="subtitle1">
                      Управление executor: {selectedExecutor.addr} ({selectedExecutor.scenario})
                    </Typography>
                    <Grid container spacing={2}>
                      <Grid size={{ xs: 4, md: 2 }}>
                        <TextField
                          fullWidth
                          type="number"
                          label="Percent"
                          value={percent}
                          onChange={(e) => setPercent(Number(e.target.value))}
                        />
                      </Grid>
                      <Grid size={{ xs: 4, md: 2 }}>
                        <TextField
                          fullWidth
                          type="number"
                          label="Base TPS"
                          value={baseTps}
                          onChange={(e) => setBaseTps(Number(e.target.value))}
                        />
                      </Grid>
                      <Grid size={{ xs: 4, md: 2 }}>
                        <TextField
                          fullWidth
                          type="number"
                          label="Ramp Up Sec"
                          value={rampUp}
                          onChange={(e) => setRampUp(Number(e.target.value))}
                        />
                      </Grid>
                    </Grid>
                    <Stack direction="row" spacing={1} flexWrap="wrap">
                      <Button
                        disabled={loading}
                        variant="contained"
                        onClick={() =>
                          runAction("/api/v1/run/start", {
                            percent,
                            base_tps: baseTps,
                            ramp_up_seconds: rampUp,
                          })
                        }
                      >
                        Запуск нагрузки
                      </Button>
                      <Button
                        disabled={loading}
                        variant="outlined"
                        onClick={() =>
                          runAction("/api/v1/run/update", {
                            percent,
                            base_tps: baseTps,
                            ramp_up_seconds: rampUp,
                          })
                        }
                      >
                        Обновить параметры
                      </Button>
                      <Button disabled={loading} variant="outlined" onClick={() => runAction("/api/v1/run/status")}>
                        Статистика
                      </Button>
                      <Button disabled={loading} variant="outlined" onClick={() => runAction("/api/v1/run/reload")}>
                        Reload
                      </Button>
                      <Button disabled={loading} variant="outlined" color="warning" onClick={() => runAction("/api/v1/run/reset-metrics")}>
                        Сброс метрик
                      </Button>
                      <Button disabled={loading} variant="contained" color="error" onClick={() => runAction("/api/v1/run/stop")}>
                        Остановить нагрузку
                      </Button>
                    </Stack>
                  </Stack>
                </CardContent>
              </Card>
            )}
          </Stack>
        </CardContent>
      </Card>

      <ResponseCard title="Executors API Response" response={lastResponse} />
    </Stack>
  );
}
