import DeleteOutlineIcon from "@mui/icons-material/DeleteOutline";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";
import PlayArrowIcon from "@mui/icons-material/PlayArrow";
import RestartAltIcon from "@mui/icons-material/RestartAlt";
import StopIcon from "@mui/icons-material/Stop";
import {
  Box,
  Button,
  Card,
  CardContent,
  Chip,
  Grid,
  IconButton,
  MenuItem,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import { Fragment, useEffect, useMemo, useRef, useState } from "react";
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
      busyWorkers: null,
      percent: null,
      baseTps: null,
      rampUpSeconds: null,
      steps: null,
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
        busyWorkers: null,
        percent: null,
        baseTps: null,
        rampUpSeconds: null,
        steps: null,
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
  const [paramDrafts, setParamDrafts] = useState({});
  const autoRefreshingRef = useRef(false);

  const count = useMemo(() => rows.length, [rows.length]);

  const rowParamDefaults = (row) => ({
    percent: row.percent ?? 100,
    baseTps: row.baseTps ?? 100,
    rampUp: row.rampUpSeconds ?? 0,
  });

  const getDisplayParams = (row) => {
    if (selectedExecutor?.pid === row.pid) {
      return { percent, baseTps, rampUp };
    }
    return paramDrafts[row.pid] ?? rowParamDefaults(row);
  };

  const updateRowParams = (row, patch) => {
    if (selectedExecutor?.pid === row.pid) {
      if ("percent" in patch) setPercent(patch.percent);
      if ("baseTps" in patch) setBaseTps(patch.baseTps);
      if ("rampUp" in patch) setRampUp(patch.rampUp);
      return;
    }
    setParamDrafts((prev) => {
      const cur = prev[row.pid] ?? rowParamDefaults(row);
      return { ...prev, [row.pid]: { ...cur, ...patch } };
    });
  };

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
        if (!executorUrl) {
          return {
            pid: row.pid,
            status: "unreachable",
            attemptsCount: null,
            successCount: null,
            errorCount: null,
            currentTps: null,
            busyWorkers: null,
            steps: null,
          };
        }
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
            busyWorkers: null,
            steps: null,
          };
        }

        const running = data.running === true;
        const metrics = data?.metrics ?? {};
        const config = data?.config ?? {};
        const attemptsCount = metrics?.attempts_count;
        const successCount = metrics?.success_count;
        const errorCount = metrics?.error_count;
        const currentTps = metrics?.current_tps;
        const busyWorkers = metrics?.busy_workers;
        const percent = config?.percent;
        const baseTps = config?.base_tps;
        const rampUpSeconds = config?.ramp_up_seconds;
        const steps = Array.isArray(metrics?.steps) ? metrics.steps : null;
        return {
          pid: row.pid,
          status: running ? "running" : "stopped",
          attemptsCount: Number.isFinite(Number(attemptsCount)) ? Number(attemptsCount) : null,
          successCount: Number.isFinite(Number(successCount)) ? Number(successCount) : null,
          errorCount: Number.isFinite(Number(errorCount)) ? Number(errorCount) : null,
          currentTps: Number.isFinite(Number(currentTps)) ? Number(currentTps).toFixed(2) : null,
          busyWorkers: Number.isFinite(Number(busyWorkers)) ? Number(busyWorkers) : null,
          percent: Number.isFinite(Number(percent)) ? Number(percent) : null,
          baseTps: Number.isFinite(Number(baseTps)) ? Number(baseTps) : null,
          rampUpSeconds: Number.isFinite(Number(rampUpSeconds)) ? Number(rampUpSeconds) : null,
          steps,
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
        busyWorkers: patch.busyWorkers,
        percent: patch.percent,
        baseTps: patch.baseTps,
        rampUpSeconds: patch.rampUpSeconds,
        steps: patch.steps ?? row.steps ?? null,
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

  const loadParamsForRow = (row) => {
    const p = getDisplayParams(row);
    return { percent: p.percent, base_tps: p.baseTps, ramp_up_seconds: p.rampUp };
  };

  const runActionForRow = async (row, path, body = {}) => {
    const executorUrl = normalizeAddr(row.addr);
    if (!executorUrl) return;
    setLoading(true);
    try {
      const response = await postApi(
        path,
        { executor_url: executorUrl, ...body },
        { baseUrl },
      );
      setLastResponse(response);
      if (path === "/api/v1/run/status") {
        await refresh({ silent: true });
      }
    } finally {
      setLoading(false);
    }
  };

  /** Play: старт с параметрами; если уже running — update параметров + status и обновление таблицы. */
  const runStartOrRefreshForRow = async (row) => {
    const executorUrl = normalizeAddr(row.addr);
    if (!executorUrl) return;
    setLoading(true);
    try {
      const params = loadParamsForRow(row);
      if (row.status === "running") {
        const updateRes = await postApi(
          "/api/v1/run/update",
          { executor_url: executorUrl, ...params },
          { baseUrl },
        );
        setLastResponse(updateRes);
        const statusRes = await postApi("/api/v1/run/status", { executor_url: executorUrl }, { baseUrl });
        setLastResponse(statusRes);
        await refresh({ silent: true });
      } else {
        if (row.status === "stopped") {
          const reloadRes = await postApi(
            "/api/v1/run/reload",
            { executor_url: executorUrl },
            { baseUrl },
          );
          setLastResponse(reloadRes);
        }
        const response = await postApi(
          "/api/v1/run/start",
          { executor_url: executorUrl, ...params },
          { baseUrl },
        );
        setLastResponse(response);
        await refresh({ silent: true });
      }
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
              <Table size="small" sx={{ minWidth: 1320 }}>
                <TableHead>
                  <TableRow>
                    <TableCell padding="checkbox" sx={{ width: 40, maxWidth: 40 }} aria-label="" />
                    <TableCell>PID</TableCell>
                    <TableCell>Config</TableCell>
                    <TableCell>Порт</TableCell>
                    <TableCell>Статус</TableCell>
                    <TableCell>Попытки</TableCell>
                    <TableCell>Успех</TableCell>
                    <TableCell>Ошибки</TableCell>
                    <TableCell>Current TPS</TableCell>
                    <TableCell>Busy Workers</TableCell>
                    <TableCell align="right" sx={{ minWidth: 560 }}>
                      Управление
                    </TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {rows.map((row) => {
                    const expanded = selectedExecutor?.pid === row.pid;
                    const rowParams = getDisplayParams(row);
                    return (
                      <Fragment key={row.pid}>
                        <TableRow
                          hover
                          selected={expanded}
                          onClick={() => {
                            const normalized = normalizeAddr(row.addr);
                            if (!normalized) {
                              return;
                            }
                            if (selectedExecutor?.pid === row.pid) {
                              setSelectedExecutor(null);
                              return;
                            }
                            setSelectedExecutor({ ...row, url: normalized });
                            onExecutorSelected?.(normalized);
                            syncControlsFromRow(row);
                          }}
                          sx={{
                            cursor: "pointer",
                            ...(expanded && {
                              "& > td": { borderBottom: 0 },
                            }),
                          }}
                        >
                          <TableCell padding="checkbox" sx={{ width: 40, maxWidth: 40, verticalAlign: "middle" }}>
                            <ExpandMoreIcon
                              fontSize="small"
                              sx={{
                                display: "block",
                                color: "action.active",
                                transform: expanded ? "rotate(180deg)" : "rotate(0deg)",
                                transition: (theme) =>
                                  theme.transitions.create("transform", {
                                    duration: theme.transitions.duration.shortest,
                                  }),
                              }}
                            />
                          </TableCell>
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
                          <TableCell>{row.busyWorkers ?? "-"}</TableCell>
                          <TableCell
                            align="right"
                            onClick={(e) => e.stopPropagation()}
                            sx={{ verticalAlign: "middle" }}
                          >
                            <Stack
                              direction="row"
                              spacing={0.25}
                              alignItems="center"
                              justifyContent="flex-end"
                              flexWrap="wrap"
                              useFlexGap
                            >
                              <>
                                <TextField
                                  size="small"
                                  type="number"
                                  label="%"
                                  value={rowParams.percent}
                                  onChange={(e) =>
                                    updateRowParams(row, { percent: Number(e.target.value) })
                                  }
                                  sx={{ width: 108, minWidth: 108, mr: 0.5 }}
                                />
                                <TextField
                                  size="small"
                                  type="number"
                                  label="TPS"
                                  value={rowParams.baseTps}
                                  onChange={(e) =>
                                    updateRowParams(row, { baseTps: Number(e.target.value) })
                                  }
                                  sx={{ width: 76, mr: 0.5 }}
                                />
                                <TextField
                                  size="small"
                                  type="number"
                                  label="Ramp"
                                  value={rowParams.rampUp}
                                  onChange={(e) =>
                                    updateRowParams(row, { rampUp: Number(e.target.value) })
                                  }
                                  sx={{ width: 72, mr: 0.5 }}
                                />
                              </>
                              <Tooltip title="Запуск нагрузки">
                                <span>
                                  <IconButton
                                    size="medium"
                                    color="primary"
                                    disabled={loading}
                                    onClick={() => runStartOrRefreshForRow(row)}
                                  >
                                    <PlayArrowIcon fontSize="large" />
                                  </IconButton>
                                </span>
                              </Tooltip>
                              <Tooltip title="Сброс метрик">
                                <span>
                                  <IconButton
                                    size="medium"
                                    color="warning"
                                    disabled={loading}
                                    onClick={() => runActionForRow(row, "/api/v1/run/reset-metrics")}
                                  >
                                    <RestartAltIcon fontSize="large" />
                                  </IconButton>
                                </span>
                              </Tooltip>
                              <Tooltip title="Остановить нагрузку">
                                <span>
                                  <IconButton
                                    size="medium"
                                    color="error"
                                    disabled={loading}
                                    onClick={() => runActionForRow(row, "/api/v1/run/stop")}
                                  >
                                    <StopIcon fontSize="large" />
                                  </IconButton>
                                </span>
                              </Tooltip>
                              <Tooltip title="Остановить процесс executor">
                                <span>
                                  <IconButton
                                    size="medium"
                                    color="error"
                                    disabled={loading}
                                    onClick={() => stopExecutor(row.pid, row.addr)}
                                  >
                                    <DeleteOutlineIcon fontSize="large" />
                                  </IconButton>
                                </span>
                              </Tooltip>
                            </Stack>
                          </TableCell>
                        </TableRow>
                        {expanded && (
                          <TableRow hover={false} selected={false}>
                            <TableCell
                              colSpan={11}
                              sx={{
                                py: 0,
                                px: 0,
                                borderTop: 0,
                                bgcolor: "action.hover",
                              }}
                              onClick={(e) => e.stopPropagation()}
                            >
                              <Box sx={{ py: 2, px: 2 }}>
                                {Array.isArray(row.steps) && row.steps.length > 0 ? (
                                  <TableContainer sx={{ width: "100%", maxWidth: "100%" }}>
                                    <Table size="small">
                                      <TableHead>
                                        <TableRow>
                                          <TableCell>#</TableCell>
                                          <TableCell>Имя</TableCell>
                                          <TableCell>Тип</TableCell>
                                          <TableCell align="right">Ошибок</TableCell>
                                          <TableCell align="right">Задержка, мс</TableCell>
                                        </TableRow>
                                      </TableHead>
                                      <TableBody>
                                        {row.steps.map((st) => (
                                          <TableRow key={`${st.index}-${st.name}`}>
                                            <TableCell>{st.index}</TableCell>
                                            <TableCell>{st.name}</TableCell>
                                            <TableCell>{st.type}</TableCell>
                                            <TableCell align="right">{st.error_count ?? 0}</TableCell>
                                            <TableCell align="right">{st.last_latency_ms ?? 0}</TableCell>
                                          </TableRow>
                                        ))}
                                      </TableBody>
                                    </Table>
                                  </TableContainer>
                                ) : (
                                  <Typography variant="body2" color="text.secondary">
                                    Нет данных по шагам (появятся после ответа executor с полем{" "}
                                    <code>metrics.steps</code>, обычно при запущенном или недавнем прогоне).
                                  </Typography>
                                )}
                              </Box>
                            </TableCell>
                          </TableRow>
                        )}
                      </Fragment>
                    );
                  })}
                </TableBody>
              </Table>
            </TableContainer>

          </Stack>
        </CardContent>
      </Card>

      <ResponseCard title="Executors API Response" response={lastResponse} />
    </Stack>
  );
}
