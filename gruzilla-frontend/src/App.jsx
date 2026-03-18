import {
  Box,
  Container,
  Drawer,
  List,
  ListItemButton,
  ListItemText,
  Stack,
  Typography,
} from "@mui/material";
import { useMemo, useState } from "react";
import BackendConnectionPanel from "./components/BackendConnectionPanel";
import ExecutorsPanel from "./components/ExecutorsPanel";
import ScenariosPanel from "./components/ScenariosPanel";
import TemplatesPanel from "./components/TemplatesPanel";

function TabPanel({ value, index, children }) {
  if (value !== index) return null;
  return <Box sx={{ mt: 2 }}>{children}</Box>;
}

export default function App() {
  const [page, setPage] = useState("backend");
  const [backendUrl, setBackendUrl] = useState("http://localhost:8080");
  const [statsRefreshSeconds, setStatsRefreshSeconds] = useState(5);

  const pages = useMemo(
    () => [
      {
        key: "backend",
        label: "Подключение",
        content: (
          <BackendConnectionPanel
            backendUrl={backendUrl}
            onBackendUrlChange={setBackendUrl}
            statsRefreshSeconds={statsRefreshSeconds}
            onStatsRefreshSecondsChange={setStatsRefreshSeconds}
          />
        ),
      },
      {
        key: "executors",
        label: "Executors",
        content: <ExecutorsPanel baseUrl={backendUrl} statsRefreshSeconds={statsRefreshSeconds} />,
      },
      {
        key: "scenarios",
        label: "Сценарии (редактирование)",
        content: <ScenariosPanel baseUrl={backendUrl} />,
      },
      {
        key: "templates",
        label: "Шаблоны",
        content: <TemplatesPanel baseUrl={backendUrl} />,
      },
    ],
    [backendUrl],
  );

  const activeIndex = Math.max(
    0,
    pages.findIndex((p) => p.key === page),
  );

  return (
    <Box sx={{ minHeight: "100vh", display: "flex" }}>
      <Container
        maxWidth={false}
        disableGutters
        sx={{ py: 3, pl: { md: "320px" }, pr: 2 }}
      >
        <Stack spacing={2}>
          <Typography variant="h4">Gruzilla Frontend</Typography>
          {pages.map((p, i) => (
            <TabPanel key={p.key} value={activeIndex} index={i}>
              {p.content}
            </TabPanel>
          ))}
        </Stack>
      </Container>

      <Drawer
        variant="permanent"
        anchor="left"
        PaperProps={{
          sx: {
            width: 300,
            boxSizing: "border-box",
            p: 1,
          },
        }}
      >
        <Stack spacing={1} sx={{ pt: 2 }}>
          <Typography variant="h6" sx={{ px: 1 }}>
            Меню
          </Typography>
          <List>
            {pages.map((p) => (
              <ListItemButton key={p.key} selected={page === p.key} onClick={() => setPage(p.key)}>
                <ListItemText primary={p.label} />
              </ListItemButton>
            ))}
          </List>
        </Stack>
      </Drawer>
    </Box>
  );
}
