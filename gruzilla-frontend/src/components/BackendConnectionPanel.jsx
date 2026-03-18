import { Card, CardContent, Stack, TextField, Typography } from "@mui/material";

export default function BackendConnectionPanel({
  backendUrl,
  onBackendUrlChange,
  statsRefreshSeconds,
  onStatsRefreshSecondsChange,
}) {
  return (
    <Card>
      <CardContent>
        <Stack spacing={2}>
          <Typography variant="h6">Backend Connection</Typography>
          <TextField
            label="Backend Base URL"
            value={backendUrl}
            onChange={(e) => onBackendUrlChange?.(e.target.value)}
            helperText="All requests are POST /api/v1/* with X-Request-Id."
            fullWidth
          />
          <TextField
            label="Интервал автообновления статистики (сек)"
            type="number"
            value={statsRefreshSeconds}
            onChange={(e) => onStatsRefreshSecondsChange?.(Math.max(1, Number(e.target.value) || 1))}
            helperText="По умолчанию: 5 сек"
            fullWidth
          />
        </Stack>
      </CardContent>
    </Card>
  );
}
