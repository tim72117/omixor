// omixor server(/console)的登入頁:整頁導去 /api/auth/web/evoleap/start,核准後導回 /console/(失敗帶 ?login_error=<代碼>)。
// 網址的 #路由 送不到伺服器,所以登入前先記在 sessionStorage,回來後還原(深連結)。
import { useEffect } from "react";
import { ErrorText } from "../components/ErrorText";
import { Logo } from "../components/Logo";
import css from "./auth.module.css";
import { loginErrorText } from "./serverLoginError";

const NEXT = "omixor.next";

function rememberRoute() {
  try {
    if (window.location.hash) sessionStorage.setItem(NEXT, window.location.hash);
  } catch {
    /* 存不了就回到首頁 */
  }
}

/** 登入後回到原本要看的畫面(只接受 #/ 開頭的站內路由) */
export function useReturnAfterLogin(loggedIn: boolean) {
  useEffect(() => {
    if (!loggedIn) return;
    try {
      const next = sessionStorage.getItem(NEXT);
      sessionStorage.removeItem(NEXT);
      if (next?.startsWith("#/") && !window.location.hash.includes("?")) window.location.hash = next;
    } catch {
      /* 沒有 sessionStorage */
    }
  }, [loggedIn]);
}

export function ServerLogin({ methods, denied }: { methods: string[]; denied?: string }) {
  useEffect(() => void (document.title = "登入 · omixor"), []);
  const error = denied ?? loginErrorText(new URLSearchParams(window.location.search).get("login_error"));
  return (
    <div className={css.bg}>
      <div className={css.card}>
        <div className={"row gap8 " + css.center}>
          <Logo size={28} />
          <span className="h2">omixor</span>
        </div>
        {methods.includes("evoleap") ? (
          <>
            <div className={"muted caption " + css.centerText}>會前往 evoleap 用 Google 帳號登入,完成後回到這裡。帳號要先在 omixor 的使用者名單裡。</div>
            <a className={"btn primary " + css.center} style={{ textDecoration: "none" }} href="/api/auth/web/evoleap/start" onClick={rememberRoute}>用 evoleap 登入</a>
          </>
        ) : (
          <div className={"muted caption " + css.centerText}>這台伺服器沒有可用的網頁登入方式。</div>
        )}
        <ErrorText>{error}</ErrorText>
      </div>
    </div>
  );
}
