import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 開發時:npm run dev(5173),/api 轉給 Go 的 omixor-web(127.0.0.1:5191)
export default defineConfig({
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    proxy: { "/api": { target: `http://127.0.0.1:${process.env.OPS_WEB_PORT ?? 5191}`, changeOrigin: true } },
  },
  build: { outDir: "dist", emptyOutDir: true, chunkSizeWarningLimit: 2000 },
});
