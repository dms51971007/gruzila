import { Box } from "@mui/material";
import { useMemo } from "react";
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  XAxis,
  YAxis,
} from "recharts";

/** Согласовано с STEP_LATENCY_CHART_BUCKETS в ExecutorsPanel. */
const CHART_BUCKETS = 30;

/** Длина окна на общем графике попыток по оси X (секунды). */
const ATTEMPT_CHART_WINDOW_SECONDS = 120;

function ceilToMultiple(value, step) {
  const v = Number(value);
  if (!Number.isFinite(v)) return step;
  return Math.ceil(v / step) * step;
}

/** Верхняя граница шкалы мини-графика задержки (мс). */
const LATENCY_SPARKLINE_Y_MAX_MS = 1000;

/**
 * Мини-график задержки по шагу (recharts).
 * `colorMode: "latency"` — step-area, линии 500/1000 мс; `fixed` — одна ступенька другим цветом.
 * `width` / `height`: число (px) или строка CSS, например `"100%"`.
 */
export function LatencySparkline({
  buckets,
  width = 368,
  height = 56,
  colorMode = "latency",
  fixedColor = "#00dd55",
  fixedStroke = "rgba(200, 240, 255, 0.9)",
  borderColor,
  showLatencyRefLines = true,
}) {
  const series = useMemo(() => {
    const b =
      Array.isArray(buckets) && buckets.length > 0 ? buckets : Array(CHART_BUCKETS).fill(0);
    return b.map((v, i) => ({ i, v: Number.isFinite(Number(v)) ? Number(v) : 0 }));
  }, [buckets]);

  const border = borderColor ?? (colorMode === "latency" ? "rgba(0,180,60,0.4)" : "rgba(100,160,200,0.45)");
  const gridStroke = colorMode === "latency" ? "rgba(0, 200, 80, 0.12)" : "rgba(120, 160, 200, 0.14)";

  return (
    <Box
      sx={{
        width,
        height,
        minWidth: width === "100%" ? 0 : undefined,
        border: "1px solid",
        borderColor: border,
        borderRadius: 0.5,
        overflow: "hidden",
        flexShrink: 0,
        bgcolor: "#0a0a0a",
        boxSizing: "border-box",
      }}
      aria-hidden
    >
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={series} margin={{ top: 0, right: 0, left: 0, bottom: 0 }}>
          <CartesianGrid strokeDasharray="4 6" stroke={gridStroke} horizontal vertical />
          <XAxis dataKey="i" type="number" domain={[0, series.length - 1]} hide />
          <YAxis domain={[0, LATENCY_SPARKLINE_Y_MAX_MS]} hide />
          {showLatencyRefLines && colorMode === "latency" ? (
            <ReferenceLine y={500} stroke="rgba(255,200,60,0.28)" strokeDasharray="3 4" />
          ) : null}
          {showLatencyRefLines && colorMode === "latency" ? (
            <ReferenceLine y={1000} stroke="rgba(255,80,80,0.32)" strokeDasharray="3 4" />
          ) : null}
          {colorMode === "latency" ? (
            <Area
              type="stepAfter"
              dataKey="v"
              stroke="rgba(210,255,225,0.92)"
              strokeWidth={1.25}
              fill="#00dd55"
              fillOpacity={0.32}
              isAnimationActive={false}
              dot={false}
            />
          ) : (
            <Area
              type="stepAfter"
              dataKey="v"
              stroke={fixedStroke}
              strokeWidth={1.15}
              fill={fixedColor}
              fillOpacity={0.35}
              isAnimationActive={false}
              dot={false}
            />
          )}
        </ComposedChart>
      </ResponsiveContainer>
    </Box>
  );
}

/** Три линии на одной шкале: прирост за опрос → на графике в событиях в секунду (recharts). */
export function AttemptMetricsCombinedChart({
  bucketsAttempts,
  bucketsSuccess,
  bucketsErrors,
  /** Секунды между опросами списка executors; из них считается перевод «за опрос» → «в секунду». */
  pollIntervalSeconds = 5,
}) {
  const { chartData, yMax } = useMemo(() => {
    const norm = (b) =>
      Array.isArray(b) && b.length === CHART_BUCKETS
        ? b.map((x) => (Number.isFinite(Number(x)) ? Number(x) : 0))
        : Array(CHART_BUCKETS).fill(0);
    const ba = norm(bucketsAttempts);
    const bs = norm(bucketsSuccess);
    const be = norm(bucketsErrors);
    const interval = Number(pollIntervalSeconds);
    const perSecond =
      Number.isFinite(interval) && interval > 0 ? 1 / interval : 1;
    const span = Math.max(1, CHART_BUCKETS - 1);
    let m = 1;
    const rows = ba.map((a, i) => {
      const attempts = a * perSecond;
      const success = bs[i] * perSecond;
      const errors = be[i] * perSecond;
      m = Math.max(m, attempts, success, errors);
      /* Слева старше (120 с), справа новее (0 с) — как «секунды назад». */
      const secInWindow = ((span - i) * ATTEMPT_CHART_WINDOW_SECONDS) / span;
      return { i, secInWindow, attempts, success, errors };
    });
    const padded = m * 1.05;
    const yMax = Math.max(5, ceilToMultiple(padded, 5));
    return { chartData: rows, yMax };
  }, [bucketsAttempts, bucketsSuccess, bucketsErrors, pollIntervalSeconds]);

  return (
    <Box
      sx={{
        width: "100%",
        maxWidth: "100%",
        height: { xs: 440, sm: 496, md: 552 },
        border: "1px solid",
        borderColor: "rgba(100, 160, 200, 0.45)",
        borderRadius: 0.5,
        overflow: "hidden",
        bgcolor: "#0a0a0a",
        boxSizing: "border-box",
      }}
      aria-hidden
    >
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={chartData} margin={{ top: 12, right: 12, left: 6, bottom: 28 }}>
          <CartesianGrid strokeDasharray="4 6" stroke="rgba(120,160,200,0.22)" horizontal vertical />
          <XAxis
            dataKey="secInWindow"
            type="number"
            domain={[0, ATTEMPT_CHART_WINDOW_SECONDS]}
            reversed
            ticks={[0, 30, 60, 90, 120]}
            allowDecimals={false}
            tick={{ fill: "rgba(170, 200, 220, 0.9)", fontSize: 11 }}
            tickLine={{ stroke: "rgba(100, 150, 190, 0.45)" }}
            axisLine={{ stroke: "rgba(100, 150, 190, 0.5)" }}
            tickFormatter={(v) => String(Math.round(Number(v)))}
            label={{
              value: "Секунды назад (0 справа = сейчас)",
              position: "bottom",
              offset: 4,
              fill: "rgba(170, 200, 220, 0.65)",
              fontSize: 11,
            }}
          />
          <YAxis
            domain={[0, yMax]}
            tick={{ fill: "rgba(170, 200, 220, 0.9)", fontSize: 11 }}
            tickLine={{ stroke: "rgba(100, 150, 190, 0.45)" }}
            axisLine={{ stroke: "rgba(100, 150, 190, 0.5)" }}
            width={44}
            tickCount={6}
            tickFormatter={(v) => {
              const x = Number(v);
              if (!Number.isFinite(x)) return "";
              const t = Math.round(x * 10) / 10;
              return Number.isInteger(t) ? String(t) : t.toFixed(1);
            }}
            label={{
              value: "В секунду",
              angle: -90,
              position: "insideLeft",
              offset: 4,
              fill: "rgba(170, 200, 220, 0.65)",
              fontSize: 11,
            }}
          />
          <Line
            type="monotone"
            dataKey="attempts"
            stroke="#5ccbff"
            strokeWidth={1.5}
            dot={false}
            isAnimationActive={false}
          />
          <Line
            type="monotone"
            dataKey="success"
            stroke="#00dd55"
            strokeWidth={1.5}
            dot={false}
            isAnimationActive={false}
          />
          <Line
            type="monotone"
            dataKey="errors"
            stroke="#ff5555"
            strokeWidth={1.5}
            dot={false}
            isAnimationActive={false}
          />
        </LineChart>
      </ResponsiveContainer>
    </Box>
  );
}
