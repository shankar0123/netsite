// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

// What: a tiny pure-SVG bar chart for one numeric column grouped
// by one categorical column. Used by the /netql route to render
// canary results without dragging in Recharts / visx for a v0
// surface.
//
// How: assumes rows[i] is [groupValue, ...numericMetrics]. We
// render the LAST column as bar height, the FIRST column as the
// label. Linear scale, no axis labels beyond the group label and
// numeric value at top. ~60 lines.
//
// Why a bespoke chart rather than Recharts: Recharts (~200 KB
// gzipped) is justified once we have multiple chart types
// (line, area, candlestick, etc.). For one bar chart in v0.0.17
// the cost-benefit doesn't pencil. Recharts arrives in v0.0.18
// alongside the canary detail timeline that needs line + zoom +
// pan.

interface BarChartProps {
  columns: string[];
  rows: unknown[][];
  height?: number;
}

export function BarChart({ columns, rows, height = 240 }: BarChartProps) {
  if (columns.length < 2 || rows.length === 0) {
    return (
      <div className="text-sm text-zinc-500 italic">
        No data to plot. Bar chart needs at least one categorical
        and one numeric column with at least one row.
      </div>
    );
  }

  const labelIdx = 0;
  const valueIdx = columns.length - 1;
  const labels = rows.map((r) => String(r[labelIdx] ?? ""));
  const values = rows.map((r) => Number(r[valueIdx] ?? 0));
  const maxVal = Math.max(...values, 0.0001);
  const w = 600;
  const padL = 60;
  const padR = 16;
  const padTop = 24;
  const padBot = 32;
  const innerW = w - padL - padR;
  const innerH = height - padTop - padBot;
  const barW = innerW / values.length;
  const accent = "var(--color-accent, #38bdf8)";

  return (
    <svg
      viewBox={`0 0 ${w} ${height}`}
      role="img"
      aria-label={`Bar chart of ${columns[valueIdx]} by ${columns[labelIdx]}`}
      className="w-full"
    >
      {/* y-axis baseline */}
      <line
        x1={padL}
        x2={w - padR}
        y1={height - padBot}
        y2={height - padBot}
        stroke="rgb(63 63 70)"
        strokeWidth={1}
      />
      {/* y-axis label (max) */}
      <text x={4} y={padTop + 4} fill="rgb(161 161 170)" fontSize={10} fontFamily="monospace">
        {maxVal.toFixed(3)}
      </text>
      <text
        x={4}
        y={height - padBot}
        fill="rgb(161 161 170)"
        fontSize={10}
        fontFamily="monospace"
      >
        0
      </text>
      {values.map((v, i) => {
        const h = (v / maxVal) * innerH;
        const x = padL + i * barW + 2;
        const y = height - padBot - h;
        return (
          <g key={i}>
            <rect x={x} y={y} width={Math.max(0, barW - 4)} height={h} fill={accent} />
            <text
              x={x + (barW - 4) / 2}
              y={height - padBot + 14}
              textAnchor="middle"
              fill="rgb(212 212 216)"
              fontSize={10}
              fontFamily="monospace"
            >
              {labels[i]}
            </text>
            <text
              x={x + (barW - 4) / 2}
              y={y - 4}
              textAnchor="middle"
              fill="rgb(244 244 245)"
              fontSize={10}
              fontFamily="monospace"
            >
              {v.toFixed(3)}
            </text>
          </g>
        );
      })}
    </svg>
  );
}
