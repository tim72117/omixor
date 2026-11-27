// 第一個 import:先把舊的 ops.* localStorage 鍵搬到 omixor.*(其他模組載入時就會讀)
import "./legacyStorage";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { AuthGate } from "./auth/AuthGate";
import { OrgGate } from "./auth/OrgGate";
import "./styles.css";

// 先 LDAP 登入(有啟用時);沒有組織時先顯示「還沒有組織」,最後進主畫面
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthGate>
      <OrgGate>
        <App />
      </OrgGate>
    </AuthGate>
  </StrictMode>,
);
