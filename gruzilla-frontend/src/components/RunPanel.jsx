import {
  Chip,
  Button,
  Card,
  CardContent,
  Grid,
  List,
  ListItemButton,
  ListItemText,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import { useEffect, useMemo, useState } from "react";
import { extractCliData, postApi } from "../api/client";
import ResponseCard from "./ResponseCard";

function addrFromUrl(url) {
  const v = String(url || "").trim();
  if (!v) return "";
  try {
    const u = new URL(v);
    if (u.port) return `:${u.port}`;
    if (u.protocol === "https:") return ":443";
    return ":80";
  } catch {
    return "";
  }
}

function urlFromAddr(addr) {
  const v = String(addr || "").trim();
  if (!v) return "";
  if (v.startsWith(":")) return `http://localhost${v}`;
  if (v.startsWith("http://") || v.startsWith("https://")) return v;
  return `http://localhost:${v}`;
}

export default function RunPanel({ baseUrl, selectedExecutorUrl }) {
  const [executorUrl, setExecutorUrl] = useState("http://localhost:8081");
  const [executorAddr, setExecutorAddr] = useState(":8081");
  const [executorBin, setExecutorBin] = useState("go");
  const [percent, setPercent] = useState(100);
  const [baseTps, setBaseTps] = useState(100);
  const [rampUp, setRampUp] = useState(0);
  const [scenarioDir, setScenarioDir] = useState("scenarios");
  const [scenarios, setScenarios] = useState([]);
  const [selectedScenario, setSelectedScenario] = useState("");
  const [executors, setExecutors] = useState([]);
  const [lastResponse, setLastResponse] = useState(null);
  const [loading, setLoading] = useState(false);

  const canControl = useMemo(() => !loading, [loading]);

  const loadScenarios = async () => {
    const response = await postApi("/api/v1/scenarios/list", { dir: scenarioDir }, { baseUrl });
    setLastResponse(response);
    const data = extractCliData(response.payload);
    if (!data) return;
    const lines = Array.isArray(data.lines) ? data.lines : [];
    setScenarios(lines);
    if (!selectedScenario && lines.length > 0) {
      setSelectedScenario(lines[0]);
    }
  };

  const loadExecutors = async () => {
    const response = await postApi("/api/v1/executors/list", {}, { baseUrl });
    setLastResponse(response);
    const data = extractCliData(response.payload);
    if (!data) return;
    const lines = Array.isArray(data.lines) ? data.lines : [];
    setExecutors(lines);
  };

  useEffect(() => {
    loadScenarios();
    loadExecutors();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [baseUrl]);

  useEffect(() => {
    if (!selectedExecutorUrl) return;
    setExecutorUrl(selectedExecutorUrl);
    const nextAddr = addrFromUrl(selectedExecutorUrl);
    if (nextAddr) setExecutorAddr(nextAddr);
  }, [selectedExecutorUrl]);

  const callRun = async (path, body = {}) => {
    setLoading(true);
    try {
      const response = await postApi(path, { executor_url: executorUrl, ...body }, { baseUrl });
      setLastResponse(response);
    } finally {
      setLoading(false);
    }
  };

  const callExecutors = async (path) => {
    if (!selectedScenario) return;
    setLoading(true);
    try {
      const response = await postApi(
        path,
        {
          scenario: selectedScenario,
          addr: executorAddr,
          bin: executorBin,
          executor_url: executorUrl,
        },
        { baseUrl },
      );
      setLastResponse(response);

      if (executorAddr.startsWith(":")) {
        setExecutorUrl(`http://localhost${executorAddr}`);
      }
      await loadExecutors();
    } finally {
      setLoading(false);
    }
  };

  return (
    <Stack spacing={2}>
      <Card>
        <CardContent>
          <Stack spacing={2}>
            <Typography variant="h6">Сценарии и управление запуском</Typography>
            <Typography variant="body2" color="text.secondary">
              Выбранный сценарий нужен для удобства оператора. Исполнение зависит от того, с каким сценарием поднят executor.
            </Typography>
            <Grid container spacing={2}>
              <Grid size={{ xs: 12, md: 4 }}>
                <TextField
                  fullWidth
                  label="Executor URL"
                  value={executorUrl}
                  onChange={(e) => {
                    const next = e.target.value;
                    setExecutorUrl(next);
                    const nextAddr = addrFromUrl(next);
                    if (nextAddr) setExecutorAddr(nextAddr);
                  }}
                  helperText={`При запуске будет использован addr=${executorAddr || "-"}`}
                />
              </Grid>
              <Grid size={{ xs: 6, md: 2 }}>
                <TextField
                  fullWidth
                  label="Executor Addr"
                  value={executorAddr}
                  onChange={(e) => {
                    const nextAddr = e.target.value;
                    setExecutorAddr(nextAddr);
                    const nextUrl = urlFromAddr(nextAddr);
                    if (nextUrl) setExecutorUrl(nextUrl);
                  }}
                  helperText="Напр. :8081"
                />
              </Grid>
              <Grid size={{ xs: 6, md: 2 }}>
                <TextField
                  fullWidth
                  label="Executor Bin"
                  value={executorBin}
                  onChange={(e) => setExecutorBin(e.target.value)}
                  helperText="go или путь"
                />
              </Grid>
              <Grid size={{ xs: 12, md: 4 }}>
                <TextField
                  fullWidth
                  label="Scenario Directory"
                  value={scenarioDir}
                  onChange={(e) => setScenarioDir(e.target.value)}
                />
              </Grid>
              <Grid size={{ xs: 12, md: 4 }}>
                <Button variant="outlined" onClick={loadScenarios} disabled={loading}>
                  Обновить список сценариев
                </Button>
              </Grid>
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

            <Card variant="outlined">
              <CardContent>
                <Stack spacing={1}>
                  <Typography variant="subtitle1">Доступные сценарии</Typography>
                  <List dense sx={{ maxHeight: 180, overflowY: "auto" }}>
                    {scenarios.map((item) => (
                      <ListItemButton key={item} selected={selectedScenario === item} onClick={() => setSelectedScenario(item)}>
                        <ListItemText primary={item} />
                      </ListItemButton>
                    ))}
                  </List>
                </Stack>
              </CardContent>
            </Card>

            <Card variant="outlined">
              <CardContent>
                <Stack spacing={1}>
                  <Stack direction="row" spacing={1} alignItems="center">
                    <Typography variant="subtitle1">Запущенные executor</Typography>
                    <Chip size="small" label={executors.length} />
                    <Button size="small" variant="outlined" onClick={loadExecutors} disabled={loading}>
                      Обновить
                    </Button>
                  </Stack>
                  <List dense sx={{ maxHeight: 180, overflowY: "auto" }}>
                    {executors.map((line) => (
                      <ListItemButton key={line}>
                        <ListItemText primary={line} />
                      </ListItemButton>
                    ))}
                  </List>
                </Stack>
              </CardContent>
            </Card>

            <Stack direction="row" spacing={1} flexWrap="wrap">
              <Button disabled={!canControl} variant="contained" onClick={() => callRun("/api/v1/run/start", { percent, base_tps: baseTps, ramp_up_seconds: rampUp })}>
                Start
              </Button>
              <Button disabled={!canControl || !selectedScenario} variant="outlined" onClick={() => callExecutors("/api/v1/executors/start")}>
                Start Executor
              </Button>
              <Button disabled={!canControl || !selectedScenario} variant="outlined" onClick={() => callExecutors("/api/v1/executors/restart")}>
                Restart Executor
              </Button>
              <Button disabled={!canControl} variant="outlined" onClick={() => callRun("/api/v1/run/update", { percent, base_tps: baseTps, ramp_up_seconds: rampUp })}>
                Update
              </Button>
              <Button disabled={!canControl} variant="outlined" onClick={() => callRun("/api/v1/run/status")}>
                Status
              </Button>
              <Button disabled={!canControl} variant="outlined" onClick={() => callRun("/api/v1/run/reload")}>
                Reload
              </Button>
              <Button disabled={!canControl} color="warning" variant="outlined" onClick={() => callRun("/api/v1/run/reset-metrics")}>
                Reset Metrics
              </Button>
              <Button disabled={!canControl} color="error" variant="contained" onClick={() => callRun("/api/v1/run/stop")}>
                Stop
              </Button>
            </Stack>
          </Stack>
        </CardContent>
      </Card>

      <ResponseCard title="Run API Response" response={lastResponse} />
    </Stack>
  );
}
