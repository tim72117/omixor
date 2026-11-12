import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { AuthGate } from "./auth/AuthGate";
import { ConnectGate } from "./auth/ConnectGate";
import "./styles.css";

// 先選連接對象(有設定遠端時),再走本機的 LDAP 登入,最後進主畫面
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ConnectGate>
      <AuthGate>
        <App />
      </AuthGate>
    </ConnectGate>
  </StrictMode>,
);
