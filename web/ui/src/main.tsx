import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { AuthGate } from "./auth/AuthGate";
import { ConnectGate } from "./auth/ConnectGate";
import { OrgGate } from "./auth/OrgGate";
import "./styles.css";

// 先選連接對象(有設定遠端時),再走本機的 LDAP 登入;遠端沒有組織時先顯示「還沒有組織」,最後進主畫面
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ConnectGate>
      <AuthGate>
        <OrgGate>
          <App />
        </OrgGate>
      </AuthGate>
    </ConnectGate>
  </StrictMode>,
);
