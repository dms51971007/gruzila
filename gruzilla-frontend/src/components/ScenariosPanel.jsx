import DescriptionOutlinedIcon from "@mui/icons-material/DescriptionOutlined";
import {
  Box,
  Button,
  Card,
  CardContent,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  Chip,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import { useEffect, useState } from "react";
import { extractCliData, extractReadFileStdout, postApi, sortPathsByFileName } from "../api/client";
import ResponseCard from "./ResponseCard";

const SCENARIOS_DIR = "scenarios";

function basenameScenario(p) {
  const s = String(p || "").replace(/\\/g, "/");
  const i = s.lastIndexOf("/");
  return i >= 0 ? s.slice(i + 1) : s;
}

/** Имя файла относительно каталога сценариев, например `my.yml`. */
function normalizeNewScenarioFileName(raw) {
  let s = String(raw || "").trim();
  if (!s) return "";
  s = basenameScenario(s);
  const lower = s.toLowerCase();
  if (!lower.endsWith(".yml") && !lower.endsWith(".yaml")) s += ".yml";
  return s;
}

function scenarioBasenameTaken(items, fileName, exceptPath) {
  const exceptBase = exceptPath ? basenameScenario(exceptPath) : "";
  return items.some((item) => {
    const b = basenameScenario(item);
    if (exceptPath && b === exceptBase) return false;
    return b === fileName;
  });
}

/** Минимальный валидный сценарий (как шаблон в gruzilla-cli scenarios create). */
function defaultScenarioYaml(fileName) {
  const base = fileName.replace(/\.(yml|yaml)$/i, "");
  const name = JSON.stringify(base);
  return `name: ${name}
description: ""
steps:
  - type: rest
    name: example-rest-step
    method: POST
    url: "http://localhost:8080/health"
    body: "{}"
`;
}

export default function ScenariosPanel({ baseUrl, showApiResponse = false }) {
  const [path, setPath] = useState("");
  const [content, setContent] = useState("");
  const [items, setItems] = useState([]);
  const [lastResponse, setLastResponse] = useState(null);
  const [loading, setLoading] = useState(false);
  const [confirmKind, setConfirmKind] = useState(null);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [createNameInput, setCreateNameInput] = useState("");
  const [renameDialogOpen, setRenameDialogOpen] = useState(false);
  const [renameNameInput, setRenameNameInput] = useState("");
  const [createError, setCreateError] = useState("");
  const [renameError, setRenameError] = useState("");

  const callApi = async (apiPath, body = {}) => {
    setLoading(true);
    try {
      const response = await postApi(apiPath, body, { baseUrl });
      setLastResponse(response);
      return response;
    } finally {
      setLoading(false);
    }
  };

  const loadList = async () => {
    const response = await callApi("/api/v1/scenarios/list", { dir: SCENARIOS_DIR });
    if (!response) return [];
    const data = extractCliData(response.payload);
    const lines = sortPathsByFileName(Array.isArray(data?.lines) ? data.lines : []);
    setItems(lines);
    if (path && !lines.includes(path)) {
      setPath("");
      setContent("");
    }
    return lines;
  };

  /** Путь как в списке API (часто `scenarios/file.yml`), по короткому имени файла. */
  const listPathForFileName = (lines, fileName) => {
    const base = basenameScenario(fileName);
    const hit = lines.find((item) => basenameScenario(item) === base);
    return hit || fileName;
  };

  const openScenario = async (selectedPath) => {
    if (!selectedPath) return;
    setPath(selectedPath);
    const response = await callApi("/api/v1/scenarios/read", { dir: SCENARIOS_DIR, path: selectedPath });
    if (!response) return;
    const data = extractCliData(response.payload);
    const text = extractReadFileStdout(data);
    if (typeof text === "string") {
      setContent(text);
    }
  };

  const handleConfirmSave = async () => {
    setConfirmKind(null);
    if (!path) return;
    await callApi("/api/v1/scenarios/update", { dir: SCENARIOS_DIR, path, content });
    await loadList();
  };

  const handleConfirmDelete = async () => {
    setConfirmKind(null);
    if (!path) return;
    await callApi("/api/v1/scenarios/delete", { dir: SCENARIOS_DIR, path, yes: true });
    setPath("");
    setContent("");
    await loadList();
  };

  const openCreateDialog = () => {
    setCreateNameInput("");
    setCreateError("");
    setCreateDialogOpen(true);
  };

  const handleCreateSubmit = async () => {
    setCreateError("");
    const fn = normalizeNewScenarioFileName(createNameInput);
    if (!fn) {
      setCreateError("Укажите имя файла.");
      return;
    }
    if (scenarioBasenameTaken(items, fn, null)) {
      setCreateError("Файл с таким именем уже есть.");
      return;
    }
    const yaml = defaultScenarioYaml(fn);
    const response = await callApi("/api/v1/scenarios/create", {
      dir: SCENARIOS_DIR,
      path: fn,
      content: yaml,
    });
    if (!response?.payload || response.payload.status !== "success" || !extractCliData(response.payload)) {
      setCreateError("Не удалось создать файл (см. ответ API ниже).");
      return;
    }
    setCreateDialogOpen(false);
    setCreateNameInput("");
    const lines = await loadList();
    const listPath = listPathForFileName(lines, fn);
    await openScenario(listPath);
  };

  const openRenameDialog = () => {
    if (!path) return;
    setRenameError("");
    setRenameNameInput(basenameScenario(path));
    setRenameDialogOpen(true);
  };

  const handleRenameSubmit = async () => {
    setRenameError("");
    if (!path) return;
    const fn = normalizeNewScenarioFileName(renameNameInput);
    if (!fn) {
      setRenameError("Укажите имя файла.");
      return;
    }
    if (basenameScenario(path) === fn) {
      setRenameDialogOpen(false);
      return;
    }
    if (scenarioBasenameTaken(items, fn, path)) {
      setRenameError("Файл с таким именем уже есть.");
      return;
    }
    const createRes = await callApi("/api/v1/scenarios/create", {
      dir: SCENARIOS_DIR,
      path: fn,
      content,
    });
    if (!createRes?.payload || createRes.payload.status !== "success" || !extractCliData(createRes.payload)) {
      setRenameError("Не удалось создать файл с новым именем (см. ответ API ниже).");
      return;
    }
    const delRes = await callApi("/api/v1/scenarios/delete", {
      dir: SCENARIOS_DIR,
      path,
      yes: true,
    });
    if (!delRes?.payload || delRes.payload.status !== "success" || !extractCliData(delRes.payload)) {
      setRenameError("Новый файл создан, но удалить старый не удалось. Обновите список и удалите дубликат вручную при необходимости.");
      await loadList();
      return;
    }
    setRenameDialogOpen(false);
    setRenameNameInput("");
    const lines = await loadList();
    const listPath = listPathForFileName(lines, fn);
    await openScenario(listPath);
  };

  useEffect(() => {
    loadList();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [baseUrl]);

  return (
    <Stack
      spacing={2}
      sx={{
        flex: 1,
        minHeight: 0,
        height: "100%",
        overflow: "hidden",
        display: "flex",
        flexDirection: "column",
      }}
    >
      <Card
        sx={{
          display: "flex",
          flexDirection: "column",
          flex: 1,
          minHeight: 0,
          overflow: "hidden",
        }}
      >
        <CardContent
          sx={{
            flex: 1,
            minHeight: 0,
            display: "flex",
            flexDirection: "column",
            overflow: "hidden",
            "&:last-child": { pb: 2 },
          }}
        >
          <Stack spacing={2} sx={{ flex: 1, minHeight: 0, overflow: "hidden" }}>
            <Typography variant="h6" sx={{ flexShrink: 0 }}>
              Сценарии
            </Typography>

            <Stack
              direction={{ xs: "column", lg: "row" }}
              spacing={2}
              alignItems="stretch"
              sx={{ flex: 1, minHeight: 0, overflow: "hidden" }}
            >
              <Card
                variant="outlined"
                sx={{
                  borderRadius: 2,
                  width: { lg: 320 },
                  flexShrink: 0,
                  display: "flex",
                  flexDirection: "column",
                  minHeight: 0,
                  maxHeight: { xs: "min(38vh, 280px)", lg: "100%" },
                  overflow: "hidden",
                }}
              >
                <CardContent
                  sx={{
                    flex: 1,
                    minHeight: 0,
                    display: "flex",
                    flexDirection: "column",
                    overflow: "hidden",
                    "&:last-child": { pb: 2 },
                  }}
                >
                  <Stack spacing={1} sx={{ flex: 1, minHeight: 0, overflow: "hidden" }}>
                    <Stack direction="row" spacing={1} alignItems="center" sx={{ flexShrink: 0 }}>
                      <Typography variant="subtitle1">Файлы</Typography>
                      <Chip size="small" label={items.length} />
                    </Stack>
                    <Typography variant="caption" color="text.secondary" sx={{ flexShrink: 0 }}>
                      Каталог: {SCENARIOS_DIR}
                    </Typography>
                    <List dense sx={{ flex: 1, minHeight: 0, overflowY: "auto", mx: -1 }}>
                      {items.map((item) => (
                        <ListItemButton
                          key={item}
                          selected={path === item}
                          sx={{ borderRadius: 1, mb: 0.25 }}
                          onClick={() => openScenario(item)}
                        >
                          <ListItemIcon sx={{ minWidth: 34 }}>
                            <DescriptionOutlinedIcon fontSize="small" />
                          </ListItemIcon>
                          <ListItemText primary={item} primaryTypographyProps={{ variant: "body2" }} />
                        </ListItemButton>
                      ))}
                    </List>
                  </Stack>
                </CardContent>
              </Card>

              <Box
                sx={{
                  flex: 1,
                  minWidth: 0,
                  minHeight: 0,
                  display: "flex",
                  flexDirection: "column",
                  overflow: "hidden",
                }}
              >
                <Card
                  variant="outlined"
                  sx={{
                    borderRadius: 2,
                    flex: 1,
                    minHeight: 0,
                    display: "flex",
                    flexDirection: "column",
                    overflow: "hidden",
                  }}
                >
                  <CardContent
                    sx={{
                      flex: 1,
                      minHeight: 0,
                      display: "flex",
                      flexDirection: "column",
                      overflow: "hidden",
                      "&:last-child": { pb: 2 },
                    }}
                  >
                    <Stack spacing={1.5} sx={{ flex: 1, minHeight: 0, overflow: "hidden" }}>
                      <Typography variant="subtitle1" sx={{ flexShrink: 0 }}>
                        {path ? `Редактирование: ${path}` : "Редактор"}
                      </Typography>
                      {!path && (
                        <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>
                          Выберите сценарий в списке слева.
                        </Typography>
                      )}
                      <TextField
                        multiline
                        fullWidth
                        minRows={4}
                        label="YAML"
                        value={content}
                        onChange={(e) => setContent(e.target.value)}
                        disabled={!path}
                        sx={{
                          flex: 1,
                          minHeight: 0,
                          display: "flex",
                          flexDirection: "column",
                          "& .MuiOutlinedInput-root": {
                            flex: 1,
                            minHeight: 0,
                            alignItems: "stretch",
                          },
                          "& textarea": {
                            height: "100% !important",
                            minHeight: "7rem !important",
                            overflowY: "auto !important",
                            resize: "none",
                            fontFamily: "monospace",
                            fontSize: 13,
                          },
                        }}
                      />
                      <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap sx={{ flexShrink: 0 }}>
                        <Button variant="outlined" disabled={loading} onClick={openCreateDialog}>
                          Создать
                        </Button>
                        <Button
                          variant="contained"
                          disabled={loading || !path}
                          onClick={() => setConfirmKind("save")}
                        >
                          Сохранить
                        </Button>
                        <Button variant="outlined" disabled={loading || !path} onClick={openRenameDialog}>
                          Переименовать
                        </Button>
                        <Button
                          color="error"
                          variant="outlined"
                          disabled={loading || !path}
                          onClick={() => setConfirmKind("delete")}
                        >
                          Удалить
                        </Button>
                      </Stack>
                    </Stack>
                  </CardContent>
                </Card>
              </Box>
            </Stack>
          </Stack>
        </CardContent>
      </Card>

      <Dialog open={confirmKind === "save"} onClose={() => !loading && setConfirmKind(null)}>
        <DialogTitle>Сохранить сценарий?</DialogTitle>
        <DialogContent>
          <DialogContentText>
            Файл «{path}» в каталоге {SCENARIOS_DIR} будет перезаписан. Продолжить?
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirmKind(null)} disabled={loading}>
            Отмена
          </Button>
          <Button variant="contained" onClick={handleConfirmSave} disabled={loading} autoFocus>
            Сохранить
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={confirmKind === "delete"} onClose={() => !loading && setConfirmKind(null)}>
        <DialogTitle>Удалить сценарий?</DialogTitle>
        <DialogContent>
          <DialogContentText>
            Будет удалён файл «{path}» из {SCENARIOS_DIR}. Это действие необратимо.
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirmKind(null)} disabled={loading}>
            Отмена
          </Button>
          <Button color="error" variant="contained" onClick={handleConfirmDelete} disabled={loading}>
            Удалить
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={createDialogOpen}
        onClose={() => !loading && setCreateDialogOpen(false)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey && !loading) {
            e.preventDefault();
            handleCreateSubmit();
          }
        }}
      >
        <DialogTitle>Новый сценарий</DialogTitle>
        <DialogContent>
          <DialogContentText sx={{ mb: 2 }}>
            Введите имя файла (например <code>load-test.yml</code>). Файл появится в каталоге{" "}
            {SCENARIOS_DIR}.
          </DialogContentText>
          <TextField
            autoFocus
            margin="dense"
            label="Имя файла"
            fullWidth
            value={createNameInput}
            onChange={(e) => setCreateNameInput(e.target.value)}
            disabled={loading}
            placeholder="my-scenario.yml"
          />
          {createError ? (
            <Typography variant="body2" color="error" sx={{ mt: 1 }}>
              {createError}
            </Typography>
          ) : null}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCreateDialogOpen(false)} disabled={loading}>
            Отмена
          </Button>
          <Button variant="contained" onClick={handleCreateSubmit} disabled={loading}>
            Создать
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={renameDialogOpen}
        onClose={() => !loading && setRenameDialogOpen(false)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey && !loading) {
            e.preventDefault();
            handleRenameSubmit();
          }
        }}
      >
        <DialogTitle>Переименовать</DialogTitle>
        <DialogContent>
          <DialogContentText sx={{ mb: 1 }}>
            Текущий файл: <strong>{path || "—"}</strong>
          </DialogContentText>
          <DialogContentText sx={{ mb: 2 }} variant="body2" color="text.secondary">
            Содержимое редактора будет записано под новым именем, затем старый файл удалён.
          </DialogContentText>
          <TextField
            autoFocus
            margin="dense"
            label="Новое имя файла"
            fullWidth
            value={renameNameInput}
            onChange={(e) => setRenameNameInput(e.target.value)}
            disabled={loading}
          />
          {renameError ? (
            <Typography variant="body2" color="error" sx={{ mt: 1 }}>
              {renameError}
            </Typography>
          ) : null}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setRenameDialogOpen(false)} disabled={loading}>
            Отмена
          </Button>
          <Button variant="contained" onClick={handleRenameSubmit} disabled={loading}>
            Переименовать
          </Button>
        </DialogActions>
      </Dialog>

      {showApiResponse ? (
        <Box sx={{ flexShrink: 0, maxHeight: "40vh", overflow: "auto" }}>
          <ResponseCard title="Scenarios API Response" response={lastResponse} />
        </Box>
      ) : null}
    </Stack>
  );
}
