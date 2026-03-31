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
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { extractCliData, postApi } from "../api/client";
import { AttemptMetricsCombinedChart, LatencySparkline } from "./metricsCharts";
import ResponseCard from "./ResponseCard";

function normalizeAddr(addr) {
  const v = String(addr || "").trim();
  if (!v) return "";
  if (v.startsWith(":")) return `http://localhost${v}`;
  if (v.startsWith("http://") || v.startsWith("https://")) return v;
  return `http://localhost:${v}`;
}

/** Адрес listen для executor (`--addr`): только порт в UI → `:8081`. */
function normalizeExecutorListenAddr(raw) {
  const v = String(raw || "").trim();
  if (!v) return "";
  if (v.startsWith(":")) return v;
  if (/^\d+$/.test(v)) return `:${v}`;
  return v;
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

/** Число точек на графике = число последних опросов (фиксированные слоты, сдвиг влево). */
const STEP_LATENCY_CHART_BUCKETS = 30;

/** Ключ истории задержек по шагу: pid + индекс шага в сценарии. */
function stepLatencyKey(pid, stepIndex) {
  return `${pid}:${stepIndex}`;
}

function normalizeStepLatencyPoints(arr) {
  if (!Array.isArray(arr) || arr.length === 0) return [];
  const first = arr[0];
  if (first != null && typeof first === "object" && "t" in first && "v" in first) {
    return arr
      .filter((p) => p && Number.isFinite(p.t) && Number.isFinite(p.v))
      .map((p) => ({
        t: p.t,
        v: p.v,
        seq: Number.isFinite(p.seq) ? p.seq : 0,
      }));
  }
  return [];
}

function emptyLatencySlots() {
  return Array(STEP_LATENCY_CHART_BUCKETS).fill(0);
}

/** Данные для графика задержки: 30 фиксированных слотов (слева старее опросы, справа новее). */
function stepLatencySlotsForChart(raw) {
  if (raw && typeof raw === "object" && Array.isArray(raw.slots) && raw.slots.length === STEP_LATENCY_CHART_BUCKETS) {
    return raw.slots;
  }
  if (Array.isArray(raw) && raw.length === STEP_LATENCY_CHART_BUCKETS && typeof raw[0] === "number") {
    return raw;
  }
  const pts = normalizeStepLatencyPoints(Array.isArray(raw) ? raw : []);
  if (pts.length === 0) return emptyLatencySlots();
  const vs = pts
    .sort((a, b) => a.t - b.t || a.seq - b.seq)
    .map((p) => p.v)
    .slice(-STEP_LATENCY_CHART_BUCKETS);
  const pad = STEP_LATENCY_CHART_BUCKETS - vs.length;
  return [...Array(pad).fill(0), ...vs];
}

function migrateStepLatencyCell(raw) {
  if (raw && typeof raw === "object" && Array.isArray(raw.slots) && raw.slots.length === STEP_LATENCY_CHART_BUCKETS) {
    return { slots: [...raw.slots], lastRefresh: raw.lastRefresh ?? null };
  }
  return { slots: stepLatencySlotsForChart(raw), lastRefresh: null };
}

/** Ключ истории счётчиков прогона (попытки / успех / ошибки) по pid. */
function attemptMetricsKey(pid) {
  return `run:${pid}`;
}

function deltaNonNeg(cur, prev) {
  if (!Number.isFinite(cur)) return 0;
  if (!Number.isFinite(prev)) return 0;
  if (cur < prev) return cur;
  return cur - prev;
}

function emptyAttemptSlots() {
  const z = emptyLatencySlots();
  return {
    attempts: [...z],
    success: [...z],
    errors: [...z],
    lastSample: null,
    lastRefresh: null,
  };
}

function migrateAttemptRunState(raw) {
  if (
    raw &&
    typeof raw === "object" &&
    Array.isArray(raw.attempts) &&
    raw.attempts.length === STEP_LATENCY_CHART_BUCKETS
  ) {
    return {
      attempts: [...raw.attempts],
      success: [...raw.success],
      errors: [...raw.errors],
      lastSample: raw.lastSample ?? null,
      lastRefresh: raw.lastRefresh ?? null,
    };
  }
  return emptyAttemptSlots();
}

function attemptSeriesSlots(state, field) {
  const st = migrateAttemptRunState(state);
  const arr = st[field];
  return Array.isArray(arr) && arr.length === STEP_LATENCY_CHART_BUCKETS ? arr : emptyLatencySlots();
}

function mergeStepLatencyHistory(rows, prev, refreshTs) {
  const next = { ...prev };
  const activePids = new Set(rows.map((r) => r.pid));
  for (const row of rows) {
    if (!Array.isArray(row.steps)) continue;
    for (const st of row.steps) {
      const k = stepLatencyKey(row.pid, st.index);
      const cell = migrateStepLatencyCell(next[k]);
      const sample = Number.isFinite(Number(st.last_latency_ms)) ? Number(st.last_latency_ms) : 0;
      let slots;
      if (cell.lastRefresh === refreshTs) {
        slots = [...cell.slots];
        slots[slots.length - 1] = sample;
      } else {
        slots = [...cell.slots.slice(1), sample];
      }
      next[k] = { slots, lastRefresh: refreshTs };
    }
  }
  for (const k of Object.keys(next)) {
    const pid = Number(String(k).split(":")[0]);
    if (!activePids.has(pid)) delete next[k];
  }
  return next;
}

function mergeAttemptMetricsHistory(rows, prev, refreshTs) {
  const next = { ...prev };
  const activePids = new Set(rows.map((r) => r.pid));
  for (const row of rows) {
    const a = row.attemptsCount;
    const s = row.successCount;
    const e = row.errorCount;
    if (a == null && s == null && e == null) continue;
    const k = attemptMetricsKey(row.pid);
    const cur = {
      attempts: Number.isFinite(Number(a)) ? Number(a) : 0,
      success: Number.isFinite(Number(s)) ? Number(s) : 0,
      errors: Number.isFinite(Number(e)) ? Number(e) : 0,
    };
    const state = migrateAttemptRunState(next[k]);
    let attempts = [...state.attempts];
    let success = [...state.success];
    let errors = [...state.errors];
    let lastRefresh = state.lastRefresh;

    if (state.lastSample != null) {
      const da = deltaNonNeg(cur.attempts, state.lastSample.attempts);
      const ds = deltaNonNeg(cur.success, state.lastSample.success);
      const de = deltaNonNeg(cur.errors, state.lastSample.errors);
      if (da !== 0 || ds !== 0 || de !== 0) {
        if (lastRefresh === refreshTs) {
          attempts[STEP_LATENCY_CHART_BUCKETS - 1] = da;
          success[STEP_LATENCY_CHART_BUCKETS - 1] = ds;
          errors[STEP_LATENCY_CHART_BUCKETS - 1] = de;
        } else {
          attempts = [...attempts.slice(1), da];
          success = [...success.slice(1), ds];
          errors = [...errors.slice(1), de];
        }
        lastRefresh = refreshTs;
      }
    }
    next[k] = { attempts, success, errors, lastSample: cur, lastRefresh };
  }
  for (const k of Object.keys(next)) {
    if (!k.startsWith("run:")) continue;
    const pid = Number(String(k).slice(4));
    if (!activePids.has(pid)) delete next[k];
  }
  return next;
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

export default function ExecutorsPanel({
  baseUrl,
  onExecutorSelected,
  statsRefreshSeconds = 5,
  showApiResponse = false,
}) {
  const [scenario, setScenario] = useState("");
  const [scenarios, setScenarios] = useState([]);
  const [addr, setAddr] = useState("8081");
  const [rows, setRows] = useState([]);
  const [selectedExecutor, setSelectedExecutor] = useState(null);
  const [percent, setPercent] = useState(100);
  const [baseTps, setBaseTps] = useState(100);
  const [rampUp, setRampUp] = useState(0);
  const [lastResponse, setLastResponse] = useState(null);
  const [loading, setLoading] = useState(false);
  const [paramDrafts, setParamDrafts] = useState({});
  /** История last_latency_ms по шагам (обновляется при каждом refresh статуса). */
  const [stepLatencyHistory, setStepLatencyHistory] = useState({});
  /** Кумулятивные attempts/success/errors по pid → графики через дельты. */
  const [attemptMetricsHistory, setAttemptMetricsHistory] = useState({});
  /** В раскрытой строке: только «шаги» или только «попытки» — переключение кнопками. */
  const [executorDetailViewByPid, setExecutorDetailViewByPid] = useState({});
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

  const applyChartHistory = useCallback((enrichedRows) => {
    const refreshTs = Date.now();
    setStepLatencyHistory((prev) => mergeStepLatencyHistory(enrichedRows, prev, refreshTs));
    setAttemptMetricsHistory((prev) => mergeAttemptMetricsHistory(enrichedRows, prev, refreshTs));
  }, []);

  /** Сброс истории всех графиков по исполнителю (шаги + попытки). */
  const clearExecutorChartsForPid = useCallback((pid) => {
    if (!Number.isFinite(Number(pid))) return;
    const prefix = `${pid}:`;
    setStepLatencyHistory((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const k of Object.keys(next)) {
        if (k.startsWith(prefix)) {
          delete next[k];
          changed = true;
        }
      }
      return changed ? next : prev;
    });
    const runKey = attemptMetricsKey(pid);
    setAttemptMetricsHistory((prev) => {
      if (!(runKey in prev)) return prev;
      const next = { ...prev };
      delete next[runKey];
      return next;
    });
  }, []);

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
      applyChartHistory(enriched);
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
    const response = await postApi("/api/v1/scenarios/list", { dir: "scenarios" }, { baseUrl });
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
      const listenAddr = normalizeExecutorListenAddr(addr);
      const response = await postApi(
        "/api/v1/executors/start",
        { scenario, addr: listenAddr, bin: "go" },
        { baseUrl },
      );
      setLastResponse(response);
      await refresh();
      const normalized = normalizeAddr(listenAddr);
      const selected = { addr: listenAddr, url: normalized, scenario };
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
      if (path === "/api/v1/run/reset-metrics" || path === "/api/v1/run/stop") {
        clearExecutorChartsForPid(row.pid);
      }
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
        clearExecutorChartsForPid(row.pid);
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

            <Stack spacing={0.5}>
              <Stack
                direction={{ xs: "column", sm: "row" }}
                spacing={2}
                alignItems={{ xs: "stretch", sm: "center" }}
                useFlexGap
              >
                <Button
                  variant="contained"
                  disabled={loading || !String(scenario || "").trim()}
                  onClick={startExecutor}
                  sx={{
                    minWidth: { sm: 160 },
                    height: 40,
                    flexShrink: 0,
                  }}
                >
                  Запустить
                </Button>
                {scenarios.length > 0 ? (
                  <TextField
                    select
                    label="Сценарий"
                    value={scenario}
                    onChange={(e) => setScenario(e.target.value)}
                    size="small"
                    sx={{ width: { xs: "100%", sm: 280 }, maxWidth: "100%", flexShrink: 0 }}
                  >
                    {scenarios.map((item) => (
                      <MenuItem key={item} value={item}>
                        {item}
                      </MenuItem>
                    ))}
                  </TextField>
                ) : (
                  <TextField
                    label="Сценарий"
                    value={scenario}
                    onChange={(e) => setScenario(e.target.value)}
                    size="small"
                    sx={{ width: { xs: "100%", sm: 280 }, maxWidth: "100%", flexShrink: 0 }}
                    placeholder="sbp-no-ssl.yml"
                  />
                )}
                <TextField
                  label="Порт"
                  value={addr}
                  onChange={(e) => setAddr(e.target.value)}
                  size="small"
                  sx={{ width: { xs: "100%", sm: 140 }, flexShrink: 0 }}
                  placeholder="8081"
                />
              </Stack>
              {scenarios.length === 0 && (
                <Typography variant="caption" color="text.secondary">
                  Список сценариев пуст — введите имя файла (например sbp-no-ssl.yml). Каталог: scenarios.
                </Typography>
              )}
            </Stack>

            <TableContainer sx={{ width: "100%", overflowX: "auto" }}>
              <Table
                size="small"
                sx={{
                  width: "100%",
                  tableLayout: "fixed",
                  minWidth: { xs: 640, sm: 800, md: 960 },
                }}
              >
                <TableHead>
                  <TableRow>
                    <TableCell padding="checkbox" sx={{ width: "3%", minWidth: 36, maxWidth: 44 }} aria-label="" />
                    <TableCell sx={{ minWidth: 44, width: "5%" }}>PID</TableCell>
                    <TableCell sx={{ minWidth: 72, width: "12%" }}>Config</TableCell>
                    <TableCell sx={{ minWidth: 64, width: "8%" }}>Порт</TableCell>
                    <TableCell sx={{ minWidth: 72, width: "8%" }}>Статус</TableCell>
                    <TableCell align="right" sx={{ minWidth: 48, width: "6%" }}>
                      Попытки
                    </TableCell>
                    <TableCell align="right" sx={{ minWidth: 44, width: "5%" }}>
                      Успех
                    </TableCell>
                    <TableCell align="right" sx={{ minWidth: 44, width: "5%" }}>
                      Ошибки
                    </TableCell>
                    <TableCell align="right" sx={{ minWidth: 56, width: "7%" }}>
                      Current TPS
                    </TableCell>
                    <TableCell align="right" sx={{ minWidth: 56, width: "7%" }}>
                      Busy Workers
                    </TableCell>
                    <TableCell align="right" sx={{ minWidth: 200, width: "34%" }}>
                      Управление
                    </TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {rows.map((row) => {
                    const expanded = selectedExecutor?.pid === row.pid;
                    const rowParams = getDisplayParams(row);
                    const attemptRunState = attemptMetricsHistory[attemptMetricsKey(row.pid)];
                    const bucketsAttempts = attemptSeriesSlots(attemptRunState, "attempts");
                    const bucketsSuccess = attemptSeriesSlots(attemptRunState, "success");
                    const bucketsErrors = attemptSeriesSlots(attemptRunState, "errors");
                    const detailView = executorDetailViewByPid[row.pid] ?? "steps";
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
                          <TableCell sx={{ minWidth: 0, maxWidth: 72, overflow: "hidden", textOverflow: "ellipsis" }}>
                            {row.pid}
                          </TableCell>
                          <TableCell
                            sx={{
                              minWidth: 0,
                              whiteSpace: "normal",
                              wordBreak: "break-word",
                              overflowWrap: "anywhere",
                            }}
                          >
                            {row.scenario}
                          </TableCell>
                          <TableCell
                            sx={{
                              minWidth: 0,
                              fontVariantNumeric: "tabular-nums",
                              overflow: "hidden",
                              textOverflow: "ellipsis",
                            }}
                          >
                            {row.addr}
                          </TableCell>
                          <TableCell sx={{ minWidth: 0 }}>
                            <Chip size="small" label={row.status} color={statusColor(row.status)} />
                          </TableCell>
                          <TableCell align="right" sx={{ minWidth: 0, fontVariantNumeric: "tabular-nums" }}>
                            {row.attemptsCount ?? "-"}
                          </TableCell>
                          <TableCell align="right" sx={{ minWidth: 0, fontVariantNumeric: "tabular-nums" }}>
                            {row.successCount ?? "-"}
                          </TableCell>
                          <TableCell align="right" sx={{ minWidth: 0, fontVariantNumeric: "tabular-nums" }}>
                            {row.errorCount ?? "-"}
                          </TableCell>
                          <TableCell align="right" sx={{ minWidth: 0, fontVariantNumeric: "tabular-nums" }}>
                            {row.currentTps ?? "-"}
                          </TableCell>
                          <TableCell align="right" sx={{ minWidth: 0, fontVariantNumeric: "tabular-nums" }}>
                            {row.busyWorkers ?? "-"}
                          </TableCell>
                          <TableCell
                            align="right"
                            onClick={(e) => e.stopPropagation()}
                            sx={{ verticalAlign: "middle", minWidth: 0 }}
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
                                <Stack direction="row" spacing={1} sx={{ mb: 2, flexWrap: "wrap", gap: 1 }}>
                                  <Button
                                    size="small"
                                    variant={detailView === "steps" ? "contained" : "outlined"}
                                    onClick={(e) => {
                                      e.stopPropagation();
                                      setExecutorDetailViewByPid((prev) => ({ ...prev, [row.pid]: "steps" }));
                                    }}
                                  >
                                    Шаги
                                  </Button>
                                  <Button
                                    size="small"
                                    variant={detailView === "attempts" ? "contained" : "outlined"}
                                    onClick={(e) => {
                                      e.stopPropagation();
                                      setExecutorDetailViewByPid((prev) => ({ ...prev, [row.pid]: "attempts" }));
                                    }}
                                  >
                                    График
                                  </Button>
                                </Stack>

                                {detailView === "steps" ? (
                                  Array.isArray(row.steps) && row.steps.length > 0 ? (
                                    <TableContainer
                                      sx={{ width: "100%", maxWidth: "100%", overflowX: "hidden" }}
                                    >
                                      <Table
                                        size="small"
                                        sx={{
                                          width: "100%",
                                          tableLayout: "fixed",
                                          "& .MuiTableCell-root": { whiteSpace: "nowrap" },
                                          "& .MuiTableCell-root.step-col-name": { whiteSpace: "normal", wordBreak: "break-word" },
                                        }}
                                      >
                                        <TableHead>
                                          <TableRow>
                                            <TableCell sx={{ width: 32, minWidth: 28, maxWidth: 40, px: 0.75 }}>
                                              #
                                            </TableCell>
                                            <TableCell
                                              sx={{ width: "14%", minWidth: 48 }}
                                              className="step-col-name"
                                            >
                                              Имя
                                            </TableCell>
                                            <TableCell sx={{ width: "8%", minWidth: 44 }}>Тип</TableCell>
                                            <TableCell align="right" sx={{ width: "7%", minWidth: 36 }}>
                                              Ошибок
                                            </TableCell>
                                            <TableCell align="right" sx={{ width: "9%", minWidth: 48 }}>
                                              Ср. за тик, мс
                                            </TableCell>
                                            <TableCell
                                              align="right"
                                              className="step-col-chart"
                                              sx={{
                                                minWidth: 0,
                                                width: "auto",
                                                overflow: "hidden",
                                              }}
                                            >
                                              График
                                            </TableCell>
                                          </TableRow>
                                        </TableHead>
                                        <TableBody>
                                          {row.steps.map((st) => {
                                            const rawHist = stepLatencyHistory[stepLatencyKey(row.pid, st.index)] || [];
                                            const sparkBuckets = stepLatencySlotsForChart(rawHist);
                                            return (
                                              <TableRow key={`${st.index}-${st.name}`}>
                                                <TableCell sx={{ width: 32, minWidth: 0, px: 0.75 }}>
                                                  {st.index}
                                                </TableCell>
                                                <TableCell className="step-col-name" sx={{ minWidth: 0 }}>
                                                  {st.name}
                                                </TableCell>
                                                <TableCell sx={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>
                                                  {st.type}
                                                </TableCell>
                                                <TableCell align="right" sx={{ minWidth: 0 }}>
                                                  {st.error_count ?? 0}
                                                </TableCell>
                                                <TableCell align="right" sx={{ minWidth: 0 }}>
                                                  <Typography
                                                    variant="body2"
                                                    component="span"
                                                    sx={{ fontVariantNumeric: "tabular-nums" }}
                                                  >
                                                    {st.last_latency_ms ?? 0}
                                                  </Typography>
                                                </TableCell>
                                                <TableCell
                                                  align="right"
                                                  className="step-col-chart"
                                                  sx={{
                                                    verticalAlign: "middle",
                                                    py: 0.5,
                                                    minWidth: 0,
                                                    width: "auto",
                                                    overflow: "hidden",
                                                    boxSizing: "border-box",
                                                  }}
                                                >
                                                  <Tooltip title="Задержка за тик (мс). 30 фиксированных точек — последние 30 опросов: сдвиг влево, справа всегда свежее значение. Пороги 500/1000 мс на линейке.">
                                                    <Box
                                                      sx={{
                                                        display: "block",
                                                        width: "100%",
                                                        maxWidth: "100%",
                                                        minWidth: 0,
                                                      }}
                                                    >
                                                      <LatencySparkline buckets={sparkBuckets} width="100%" />
                                                    </Box>
                                                  </Tooltip>
                                                </TableCell>
                                              </TableRow>
                                            );
                                          })}
                                        </TableBody>
                                      </Table>
                                    </TableContainer>
                                  ) : (
                                    <Typography variant="body2" color="text.secondary">
                                      Нет данных по шагам (появятся после ответа executor с полем{" "}
                                      <code>metrics.steps</code>, обычно при запущенном или недавнем прогоне).
                                    </Typography>
                                  )
                                ) : (
                                  <Box sx={{ width: "100%", maxWidth: "100%", overflow: "hidden" }}>
                                    <Typography variant="caption" color="text.secondary" display="block" sx={{ mb: 1 }}>
                                      По горизонтали — 2 минуты, шкала «секунд назад»: слева 120, справа 0 (сейчас); по
                                      вертикали — попытки, успех и ошибки в секунду (интервал опроса {statsRefreshSeconds}{" "}
                                      с). 30 точек.
                                    </Typography>
                                    <Stack direction="row" spacing={2} alignItems="center" flexWrap="wrap" sx={{ mb: 1 }}>
                                      <Stack direction="row" spacing={0.5} alignItems="center">
                                        <Box sx={{ width: 12, height: 12, bgcolor: "#5ccbff", borderRadius: 0.25 }} />
                                        <Typography variant="caption">Попытки</Typography>
                                      </Stack>
                                      <Stack direction="row" spacing={0.5} alignItems="center">
                                        <Box sx={{ width: 12, height: 12, bgcolor: "#00dd55", borderRadius: 0.25 }} />
                                        <Typography variant="caption">Успех</Typography>
                                      </Stack>
                                      <Stack direction="row" spacing={0.5} alignItems="center">
                                        <Box sx={{ width: 12, height: 12, bgcolor: "#ff5555", borderRadius: 0.25 }} />
                                        <Typography variant="caption">Ошибки</Typography>
                                      </Stack>
                                    </Stack>
                                    <AttemptMetricsCombinedChart
                                      bucketsAttempts={bucketsAttempts}
                                      bucketsSuccess={bucketsSuccess}
                                      bucketsErrors={bucketsErrors}
                                      pollIntervalSeconds={statsRefreshSeconds}
                                    />
                                  </Box>
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

      {showApiResponse ? <ResponseCard title="Executors API Response" response={lastResponse} /> : null}
    </Stack>
  );
}
