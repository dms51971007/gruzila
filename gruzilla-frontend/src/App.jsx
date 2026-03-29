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

function TabPanel({ value, index, children, fullHeight }) {
  if (value !== index) return null;
  return (
    <Box
      sx={{
        mt: 2,
        ...(fullHeight
          ? {
              flex: 1,
              minHeight: 0,
              display: "flex",
              flexDirection: "column",
              overflow: "hidden",
            }
          : {}),
      }}
    >
      {children}
    </Box>
  );
}

export default function App() {
  const [page, setPage] = useState("executors");
  const [backendUrl, setBackendUrl] = useState("http://localhost:8080");
  const [statsRefreshSeconds, setStatsRefreshSeconds] = useState(5);
  const [showApiResponse, setShowApiResponse] = useState(false);

  const pages = useMemo(
    () => [
      {
        key: "executors",
        label: "Executors",
        content: (
          <ExecutorsPanel
            baseUrl={backendUrl}
            statsRefreshSeconds={statsRefreshSeconds}
            showApiResponse={showApiResponse}
          />
        ),
      },
      {
        key: "scenarios",
        label: "Сценарии",
        content: <ScenariosPanel baseUrl={backendUrl} showApiResponse={showApiResponse} />,
      },
      {
        key: "templates",
        label: "Шаблоны",
        content: <TemplatesPanel baseUrl={backendUrl} showApiResponse={showApiResponse} />,
      },
      {
        key: "backend",
        label: "Настройки",
        content: (
          <BackendConnectionPanel
            backendUrl={backendUrl}
            onBackendUrlChange={setBackendUrl}
            statsRefreshSeconds={statsRefreshSeconds}
            onStatsRefreshSecondsChange={setStatsRefreshSeconds}
            showApiResponse={showApiResponse}
            onShowApiResponseChange={setShowApiResponse}
          />
        ),
      },
    ],
    [backendUrl, statsRefreshSeconds, showApiResponse],
  );

  const activeIndex = Math.max(
    0,
    pages.findIndex((p) => p.key === page),
  );

  return (
    <Box sx={{ height: "100vh", minHeight: "100vh", display: "flex", flexDirection: "row", overflow: "hidden" }}>
      <Container
        maxWidth={false}
        disableGutters
        sx={{
          flex: 1,
          minWidth: 0,
          minHeight: 0,
          display: "flex",
          flexDirection: "column",
          py: 3,
          pl: { md: "320px" },
          pr: 2,
        }}
      >
        <Stack spacing={2} sx={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
          <Typography variant="h4" sx={{ flexShrink: 0 }}>
            Gruzilla Frontend
          </Typography>
          {pages.map((p, i) => (
            <TabPanel key={p.key} value={activeIndex} index={i} fullHeight={p.key === "scenarios" || p.key === "templates"}>
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
