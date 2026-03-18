import {
  Chip,
  Button,
  Card,
  CardContent,
  InputAdornment,
  Grid,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import DescriptionOutlinedIcon from "@mui/icons-material/DescriptionOutlined";
import SearchIcon from "@mui/icons-material/Search";
import { useEffect, useState } from "react";
import { extractCliData, postApi } from "../api/client";
import ResponseCard from "./ResponseCard";

export default function TemplatesPanel({ baseUrl }) {
  const [dir, setDir] = useState("templates");
  const [path, setPath] = useState("");
  const [content, setContent] = useState("{\"requestId\":\"{{requestId}}\"}");
  const [items, setItems] = useState([]);
  const [query, setQuery] = useState("");
  const [lastResponse, setLastResponse] = useState(null);
  const [loading, setLoading] = useState(false);

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
    const response = await callApi("/api/v1/templates/list", { dir });
    if (!response) return;
    const data = extractCliData(response.payload);
    const lines = Array.isArray(data?.lines) ? data.lines : [];
    setItems(lines);
    if (!path && lines.length > 0) {
      setPath(lines[0]);
    }
  };

  const readSelected = async (selectedPath = path) => {
    if (!selectedPath) return;
    const response = await callApi("/api/v1/templates/read", { dir, path: selectedPath });
    if (!response) return;
    const data = extractCliData(response.payload);
    const text = data?.stdout;
    if (typeof text === "string") {
      setContent(text);
    }
  };

  useEffect(() => {
    loadList();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [baseUrl]);

  const filtered = items.filter((item) => item.toLowerCase().includes(query.toLowerCase()));

  return (
    <Stack spacing={2}>
      <Card>
        <CardContent>
          <Stack spacing={2}>
            <Typography variant="h6">Шаблоны: список и редактирование</Typography>
            <Grid container spacing={2}>
              <Grid size={{ xs: 12, md: 3 }}>
                <TextField label="Directory" value={dir} onChange={(e) => setDir(e.target.value)} fullWidth />
              </Grid>
              <Grid size={{ xs: 12, md: 3 }}>
                <TextField label="Path" value={path} onChange={(e) => setPath(e.target.value)} fullWidth />
              </Grid>
              <Grid size={{ xs: 12, md: 6 }}>
                <TextField
                  label="Поиск шаблона"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  fullWidth
                  InputProps={{
                    startAdornment: (
                      <InputAdornment position="start">
                        <SearchIcon />
                      </InputAdornment>
                    ),
                  }}
                />
              </Grid>
            </Grid>

            <Grid container spacing={2}>
              <Grid size={{ xs: 12, md: 5 }}>
                <Card variant="outlined" sx={{ borderRadius: 3 }}>
                  <CardContent>
                    <Stack spacing={1.5}>
                      <Stack direction="row" spacing={1} alignItems="center">
                        <Typography variant="subtitle1">Список шаблонов</Typography>
                        <Chip size="small" label={filtered.length} />
                      </Stack>
                      <Stack direction="row" spacing={1}>
                        <Button disabled={loading} variant="outlined" onClick={loadList}>
                          Обновить
                        </Button>
                        <Button disabled={loading || !path} variant="outlined" onClick={() => readSelected()}>
                          Открыть
                        </Button>
                      </Stack>
                      <List dense sx={{ maxHeight: 320, overflowY: "auto", pr: 1 }}>
                        {filtered.map((item) => (
                          <ListItemButton
                            key={item}
                            selected={path === item}
                            sx={{ borderRadius: 2, mb: 0.5 }}
                            onClick={() => {
                              setPath(item);
                              readSelected(item);
                            }}
                          >
                            <ListItemIcon sx={{ minWidth: 34 }}>
                              <DescriptionOutlinedIcon fontSize="small" />
                            </ListItemIcon>
                            <ListItemText primary={item} />
                          </ListItemButton>
                        ))}
                      </List>
                    </Stack>
                  </CardContent>
                </Card>
              </Grid>

              <Grid size={{ xs: 12, md: 7 }}>
                <Card variant="outlined" sx={{ borderRadius: 3 }}>
                  <CardContent>
                    <Stack spacing={1.5}>
                      <Typography variant="subtitle1">Редактор шаблона</Typography>
                      <TextField
                        multiline
                        minRows={14}
                        label="Template Content (for create/update)"
                        value={content}
                        onChange={(e) => setContent(e.target.value)}
                      />
                      <Stack direction="row" spacing={1} flexWrap="wrap">
                        <Button disabled={loading} variant="contained" onClick={() => callApi("/api/v1/templates/create", { dir, path, content })}>
                          Create
                        </Button>
                        <Button disabled={loading} variant="contained" onClick={() => callApi("/api/v1/templates/update", { dir, path, content })}>
                          Update
                        </Button>
                        <Button
                          disabled={loading}
                          color="error"
                          variant="outlined"
                          onClick={() => callApi("/api/v1/templates/delete", { dir, path, yes: true })}
                        >
                          Delete
                        </Button>
                      </Stack>
                    </Stack>
                  </CardContent>
                </Card>
              </Grid>
            </Grid>
          </Stack>
        </CardContent>
      </Card>

      <ResponseCard title="Templates API Response" response={lastResponse} />
    </Stack>
  );
}
