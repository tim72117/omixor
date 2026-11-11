// SF Symbols 名稱(後端資料與 Swift 版共用)→ lucide 圖示。找不到的用圓點。
import * as L from "lucide-react";
import type { LucideIcon } from "lucide-react";

const MAP: Record<string, LucideIcon> = {
  // 監控、事件、AI 自動化、文件櫃、設定、助理用到的
  "chart.xyaxis.line": L.ChartLine, "list.bullet.rectangle": L.ClipboardList, "person.3": L.Users, "exclamationmark.octagon": L.OctagonX,
  "xmark.octagon": L.OctagonX, "info.circle": L.Info, "line.3.horizontal.decrease.circle": L.ListFilter, function: L.SquareFunction,
  number: L.Hash, phone: L.Phone, envelope: L.Mail, message: L.MessageSquare, "dot.radiowaves.left.and.right": L.Radio,
  "point.3.connected.trianglepath.dotted": L.Waypoints, "text.magnifyingglass": L.TextSearch, "questionmark.circle": L.CircleHelp,
  "square.dashed": L.Square, "wrench.and.screwdriver": L.Wrench, scope: L.Crosshair, flag: L.Flag, "text.bubble": L.MessageSquareText,
  timer: L.Timer, "hand.thumbsup": L.ThumbsUp, "arrow.up.forward.circle": L.CircleArrowUp, "checkmark.square": L.SquareCheck, square: L.Square,
  calendar: L.Calendar, "calendar.badge.exclamationmark": L.CalendarX, "iphone.radiowaves.left.and.right": L.Smartphone,
  "phone.arrow.up.right": L.PhoneForwarded, "person.badge.shield.checkmark": L.ShieldCheck, "checkmark.shield": L.ShieldCheck,
  "arrow.right.circle": L.CircleArrowRight, paperplane: L.Send, "arrow.up": L.ArrowUp, "arrow.down": L.ArrowDown, "doc.on.clipboard": L.ClipboardCopy,
  checkmark: L.Check, "arrow.up.right": L.ArrowUpRight, "bell.and.waves.left.and.right": L.BellRing, "chevron.left": L.ChevronLeft,
  "tray.full": L.Inbox, tray: L.Inbox, hourglass: L.Hourglass, "hourglass.bottomhalf.filled": L.Hourglass, lightbulb: L.Lightbulb,
  "shield.lefthalf.filled": L.ShieldHalf, terminal: L.Terminal, "arrow.uturn.backward.circle": L.Undo2, "checkmark.seal": L.BadgeCheck,
  "xmark.seal": L.BadgeX, pencil: L.Pencil, "pencil.circle": L.Pencil, "eye.circle": L.Eye, "bolt.horizontal.circle": L.Zap, "wrench.adjustable": L.Wrench,
  cube: L.Box, "play.circle": L.CirclePlay, "dollarsign.circle": L.CircleDollarSign, "text.word.spacing": L.Type, speedometer: L.Gauge,
  nosign: L.Ban, "hand.raised.slash": L.HandMetal, stethoscope: L.Stethoscope, "arrow.triangle.2.circlepath": L.RefreshCw,
  "sidebar.left": L.PanelLeft, "sidebar.trailing": L.PanelRight, "arrow.uturn.right.circle": L.Redo2, "circle.dotted": L.CircleDashed,
  "puzzlepiece.extension": L.Puzzle, power: L.Power, moon: L.Moon, "list.bullet.clipboard": L.ClipboardList, "square.and.arrow.up": L.Upload,
  "ellipsis.circle": L.Ellipsis, briefcase: L.Briefcase, storefront: L.Store, flask: L.FlaskConical, heart: L.Heart, leaf: L.Leaf, star: L.Star,
  "folder.badge.plus": L.FolderPlus, "minus.circle": L.CircleMinus, "chevron.up.chevron.down": L.ChevronsUpDown, "brain.head.profile": L.Brain,
  "arrow.up.circle": L.CircleArrowUp, "person.crop.circle.badge.exclamationmark": L.UserX, "arrow.up.left.and.arrow.down.right": L.Scale,
  "doc.text.magnifyingglass": L.FileSearch, "dollarsign.arrow.circlepath": L.CircleDollarSign, "shippingbox.and.arrow.backward": L.Undo2,
  "trash.circle": L.Trash2, "bubble.left.and.bubble.right": L.MessagesSquare, creditcard: L.CreditCard, "phone.badge.waveform": L.PhoneCall,
  "person.crop.circle.badge.plus": L.UserPlus, "xmark": L.X, "dot.radiowaves.right": L.Radio, "hammer.fill": L.Hammer, "play": L.Play,
  "square.grid.2x2": L.LayoutGrid, hammer: L.Hammer, "arrow.triangle.branch": L.GitBranch, "server.rack": L.Server,
  "waveform.path.ecg": L.Activity, flame: L.Flame, sparkles: L.Sparkles, archivebox: L.Archive, gearshape: L.Settings,
  "building.2": L.Building2, graduationcap: L.GraduationCap, "bell.badge": L.BellRing, "bell.slash": L.BellOff, bell: L.Bell,
  "checkmark.circle": L.CircleCheck, "checkmark.circle.fill": L.CircleCheck, "xmark.circle": L.CircleX, "xmark.circle.fill": L.CircleX,
  "exclamationmark.triangle": L.TriangleAlert, "exclamationmark.triangle.fill": L.TriangleAlert, "exclamationmark.bubble": L.MessageSquareWarning,
  "arrow.triangle.pull": L.GitPullRequest, shippingbox: L.Package, "chart.bar.xaxis": L.ChartColumn, "rectangle.split.3x1": L.Columns3,
  "person.badge.key": L.KeyRound, "key.horizontal": L.KeyRound, trash: L.Trash2, "arrow.left.arrow.right": L.ArrowLeftRight,
  "doc.text": L.FileText, doc: L.File, folder: L.Folder, tag: L.Tag, plus: L.Plus, "arrow.clockwise": L.RefreshCw,
  "magnifyingglass": L.Search, "person.crop.circle": L.CircleUser, "person.crop.circle.badge.checkmark": L.UserCheck,
  cloud: L.Cloud, "chevron.left.forwardslash.chevron.right": L.Code, "clock": L.Clock, "clock.arrow.circlepath": L.History,
  "slash.circle": L.Ban, "circle.dashed": L.CircleDashed, "gearshape.2": L.Cog, "doc.on.doc": L.Copy,
  "slider.horizontal.3": L.SlidersHorizontal, "square.stack.3d.up": L.Layers, "rocket": L.Rocket, "arrow.uturn.backward": L.Undo2,
  "play.fill": L.Play, "stop.fill": L.Square, "pause.fill": L.Pause, "waveform": L.AudioLines, "speaker.wave.2": L.Volume2,
  "person.2": L.Users, person: L.User, "hand.raised": L.Hand, "checklist": L.ListChecks, "list.bullet": L.List,
  "bolt": L.Zap, "chevron.down": L.ChevronDown, "chevron.right": L.ChevronRight, "shield": L.Shield, "lock": L.Lock, "link": L.Link, "globe": L.Globe, "cpu": L.Cpu, "memorychip": L.MemoryStick,
  "internaldrive": L.HardDrive, "network": L.Network, "chart.line.uptrend.xyaxis": L.TrendingUp, "text.alignleft": L.AlignLeft,
  "note.text": L.NotebookText, "megaphone": L.Megaphone, "book": L.Book, "eye": L.Eye, "wrench": L.Wrench, "ant": L.Bug,
};

export function Icon({ name, size = 14, className, color }: { name?: string; size?: number; className?: string; color?: string }) {
  const C = (name && (MAP[name] ?? MAP[name.replace(/\.fill$/, "")])) || L.Circle;
  return <C size={size} className={className} color={color} strokeWidth={1.8} style={{ flex: "none" }} />;
}
