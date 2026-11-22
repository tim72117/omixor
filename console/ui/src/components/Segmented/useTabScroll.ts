import { useEffect, useRef } from "react";

/**
 * 分頁列放不下時的橫向捲動輔助:元素的父元素是 .tabbar 或 .tabscroll(全域 class,可橫向捲動)時,
 * 依捲動位置在父元素設 data-fade-l / data-fade-r(CSS 用來淡出邊緣、提示還有分頁),並在選取改變時把選中的項目
 * (selected 選擇器)捲進可視範圍。捲軸隱藏(Segmented.module.css),所以滑鼠滾輪的直向捲動也轉成橫向捲動
 * (捲到底時不攔,讓頁面照常捲)。父元素不是這兩種時什麼都不做。
 */
export function useTabScroll<E extends HTMLElement>(value: unknown, selected = "button.on") {
  const ref = useRef<E>(null);
  const scroller = () => {
    const sc = ref.current?.parentElement;
    return sc && (sc.classList.contains("tabbar") || sc.classList.contains("tabscroll")) ? sc : null;
  };
  useEffect(() => {
    const el = ref.current;
    const sc = scroller();
    if (!el || !sc) return;
    const update = () => {
      const max = sc.scrollWidth - sc.clientWidth;
      sc.toggleAttribute("data-fade-l", max > 1 && sc.scrollLeft > 1);
      sc.toggleAttribute("data-fade-r", max > 1 && sc.scrollLeft < max - 1);
    };
    update();
    sc.addEventListener("scroll", update, { passive: true });
    // 滑鼠滾輪(直向為主)→ 橫向捲動;觸控板本來就有橫向位移,不處理
    const wheel = (e: WheelEvent) => {
      if (e.ctrlKey || Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
      const max = sc.scrollWidth - sc.clientWidth;
      if (max <= 1) return;
      const dy = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
      if ((dy < 0 && sc.scrollLeft <= 0) || (dy > 0 && sc.scrollLeft >= max - 1)) return;
      e.preventDefault();
      sc.scrollLeft += dy;
    };
    sc.addEventListener("wheel", wheel, { passive: false });
    const ro = new ResizeObserver(update);
    ro.observe(sc);
    ro.observe(el);
    return () => { sc.removeEventListener("scroll", update); sc.removeEventListener("wheel", wheel); ro.disconnect(); sc.removeAttribute("data-fade-l"); sc.removeAttribute("data-fade-r"); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    const sc = scroller();
    const on = ref.current?.querySelector<HTMLElement>(selected);
    if (!sc || !on) return;
    // 只捲這個分頁列(不用 scrollIntoView,免得連帶捲動整頁)
    const r = on.getBoundingClientRect(), s = sc.getBoundingClientRect(), pad = 36;
    if (r.left < s.left + pad) sc.scrollLeft -= s.left + pad - r.left;
    else if (r.right > s.right - pad) sc.scrollLeft += r.right - (s.right - pad);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);
  return ref;
}
