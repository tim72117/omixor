// 動作完成的短暫提示(InfraToast)與「執行動作、失敗顯示 toast」(原本在 omixorKit.tsx)
import { useRef, useState, type ReactNode } from "react";
import { SIcon } from "../SIcon";
import s from "./Toast.module.css";

export function useToast(): [ReactNode, (text: string, kind?: "ok" | "error") => void] {
  const [msg, setMsg] = useState<{ text: string; kind: "ok" | "error" } | null>(null);
  const timer = useRef<number>();
  const show = (text: string, kind: "ok" | "error" = "ok") => {
    setMsg({ text, kind });
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setMsg(null), kind === "error" ? 5000 : 2600);
  };
  const el = msg ? (
    <div className={s.toast}>
      <SIcon name={msg.kind === "ok" ? "checkmark.circle.fill" : "exclamationmark.triangle.fill"} color={msg.kind === "ok" ? "green" : "orange"} />
      <span>{msg.text}</span>
    </div>
  ) : null;
  return [el, show];
}

/** 執行動作;失敗時用 toast 顯示錯誤(後端還沒做好的動作會顯示「沒有這個 action」之類的訊息) */
export function useAct() {
  const [toastEl, toast] = useToast();
  const run = async <T,>(f: () => Promise<T>, ok?: string): Promise<T | undefined> => {
    try {
      const r = await f();
      if (ok) toast(ok);
      return r;
    } catch (e: any) {
      toast(String(e?.message ?? e), "error");
      return undefined;
    }
  };
  return { run, toast, toastEl };
}
