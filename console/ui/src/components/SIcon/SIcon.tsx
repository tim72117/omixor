// Icon.tsx 的擴充:補 Icon 沒有的 SF Symbols(lucide),並支援 color 名稱與旋轉(原本在 omixorKit.tsx)
import type { CSSProperties } from "react";
import * as L from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { color } from "../../lib/color";
import { Icon } from "../Icon";
import s from "./SIcon.module.css";

const EXTRA: Record<string, LucideIcon> = {
  "checkmark.shield": L.ShieldCheck, speedometer: L.Gauge, "checkmark.seal": L.BadgeCheck, "checkmark.seal.fill": L.BadgeCheck,
  "xmark.seal": L.BadgeX, "xmark.octagon": L.OctagonX, "xmark.octagon.fill": L.OctagonX, "wrench.and.screwdriver": L.Wrench,
  "questionmark.circle": L.CircleHelp, "questionmark.circle.fill": L.CircleHelp, "arrow.triangle.2.circlepath": L.RefreshCw,
  "hand.raised.fill": L.Hand, "circle.dotted": L.CircleDotDashed, tortoise: L.Turtle, "person.badge.clock": L.UserRoundSearch,
  "gauge.with.dots.needle.33percent": L.Gauge, "list.bullet.rectangle": L.ListTodo, "flag.checkered": L.Flag, timer: L.Timer,
  hourglass: L.Hourglass, "flag.2.crossed.fill": L.Flag, "chart.line.downtrend.xyaxis": L.TrendingDown, "chart.bar": L.ChartColumn,
  tray: L.Inbox, circle: L.Circle, "circle.lefthalf.filled": L.Contrast, "arrow.up.forward": L.ArrowUpRight, "arrow.right": L.ArrowRight,
  "smallcircle.filled.circle": L.Dot, "ellipsis.circle": L.Ellipsis, ellipsis: L.Ellipsis, xmark: L.X, "arrow.triangle.merge": L.GitMerge,
  "exclamationmark.shield.fill": L.ShieldAlert, "shippingbox.fill": L.Package, "bubble.left": L.MessageSquare, scope: L.Crosshair,
  "stop.circle": L.CircleStop, "wand.and.stars": L.WandSparkles, terminal: L.SquareTerminal, "doc.text.magnifyingglass": L.FileSearch,
  number: L.Hash, bird: L.Bird, "square.split.2x1": L.Columns2, "arrow.uturn.backward.circle": L.RotateCcw,
  "arrow.down.circle.fill": L.CircleArrowDown, "arrow.up.forward.circle.fill": L.CircleArrowOutUpRight, "cross.case": L.BriefcaseMedical,
  "paperplane.fill": L.Send, "play.circle.fill": L.CirclePlay, "stop.circle.fill": L.CircleStop, "person.3": L.Users, "book.closed": L.BookOpen,
  "arrow.up.left.and.arrow.down.right": L.Maximize2, "drop.triangle": L.Droplet, restart: L.RotateCw, "arrow.uturn.left": L.Undo2,
  "arrow.right.circle": L.CircleArrowRight, "line.3.horizontal.decrease.circle": L.ListFilter, "line.3.horizontal.decrease.circle.fill": L.ListFilter,
  "point.3.connected.trianglepath.dotted": L.Workflow, "square.stack.3d.up.slash": L.Layers, "arrow.up.circle": L.CircleArrowUp,
  "sidebar.left": L.PanelLeft, "lock.shield": L.ShieldCheck, "gearshape.fill": L.Settings, power: L.Power, "rectangle.stack": L.Layers,
  "arrow.down": L.ArrowDown, "arrow.uturn.backward.circle.fill": L.RotateCcw, stethoscope: L.Stethoscope, "person.crop.circle.badge.checkmark": L.UserCheck,
  "exclamationmark.bubble": L.MessageSquareWarning, "arrow.triangle.pull": L.GitPullRequest, "chevron.left.forwardslash.chevron.right": L.Code,
};

export function SIcon({ name, size = 14, color: c, className, spin, style }: { name?: string; size?: number; color?: string; className?: string; spin?: boolean; style?: CSSProperties }) {
  const C = name ? EXTRA[name] : undefined;
  const cls = (className ?? "") + (spin ? " " + s.spin : "");
  if (C) return <C size={size} color={c ? color(c) : undefined} className={cls} strokeWidth={1.8} style={{ flex: "none", ...style }} />;
  return <span className={cls} style={{ display: "inline-flex", ...style }}><Icon name={name} size={size} color={c ? color(c) : undefined} /></span>;
}
