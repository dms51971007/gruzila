import { Alert, Card, CardContent, Stack, Typography } from "@mui/material";

export default function ResponseCard({ title, response }) {
  if (!response) return null;

  const isError = response.payload?.status === "error" || !response.ok;
  const body = response.payload ?? {};

  return (
    <Card variant="outlined">
      <CardContent>
        <Stack spacing={1.5}>
          <Typography variant="subtitle1">{title}</Typography>
          <Typography variant="body2" color="text.secondary">
            HTTP: {response.status} | request-id: {response.requestId}
          </Typography>
          {isError && (
            <Alert severity="error">
              {body.error || "Request failed"}
            </Alert>
          )}
          <pre style={{ margin: 0, overflowX: "auto" }}>
            {JSON.stringify(body, null, 2)}
          </pre>
        </Stack>
      </CardContent>
    </Card>
  );
}
