import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 開發時:npm run dev(5173),/api 轉給 Go 的 omixor-web(127.0.0.1:5191)
// OMIXOR_UI_BASE:畫面掛在哪個路徑。console 用預設 "/";omixor server 的映像建置時給 "/console/"(server/webui.go)
export default defineConfig({
  base: process.env.OMIXOR_UI_BASE || "/",
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    proxy: { "/api": { target: `http://127.0.0.1:${process.env.OMIXOR_WEB_PORT ?? process.env.OPS_WEB_PORT ?? 5191}`, changeOrigin: true } },
  },
  build: { outDir: "dist", emptyOutDir: true, chunkSizeWarningLimit: 2000 },
});
