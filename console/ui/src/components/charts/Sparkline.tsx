// 迷你面積圖(recharts)
import { useState } from "react";
import { Area, AreaChart, ResponsiveContainer, YAxis } from "recharts";
import { color } from "../../lib/color";

let sparkId = 0;
export function Sparkline({ values, c = "blue", height = 28 }: { values: number[]; c?: string; height?: number }) {
  const [id] = useState(() => `sp${++sparkId}`);
  if (values.length < 2) return <div style={{ height }} />;
  const data = values.map((v, i) => ({ i, v }));
  const col = color(c);
  return (
    <div style={{ height, width: "100%" }}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 2, bottom: 2, left: 0, right: 0 }}>
          <defs>
            <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={col} stopOpacity={0.25} />
              <stop offset="100%" stopColor={col} stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <YAxis hide domain={["dataMin", "dataMax"]} />
          <Area type="monotone" dataKey="v" stroke={col} strokeWidth={1.5} fill={`url(#${id})`} isAnimationActive={false} />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
