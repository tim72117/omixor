import { useEffect, useState } from "react";

/** 複製文字;沒有 clipboard API(非 https)時退回 execCommand */
export async function copyText(s: string) {
  try {
    await navigator.clipboard.writeText(s);
  } catch {
    const t = document.createElement("textarea");
    t.value = s;
    document.body.appendChild(t);
    t.select();
    document.execCommand("copy");
    t.remove();
  }
}

/** 複製後顯示「已複製」1.5 秒:const [copied, copy] = useCopied(); copy(key, text) */
export function useCopied(): [string | undefined, (key: string, text: string) => void] {
  const [k, setK] = useState<string>();
  useEffect(() => {
    if (!k) return;
    const t = window.setTimeout(() => setK(undefined), 1600);
    return () => window.clearTimeout(t);
  }, [k]);
  return [k, (key, text) => { copyText(text); setK(key); }];
}
