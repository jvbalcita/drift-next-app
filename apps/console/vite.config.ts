import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

const srcDir = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(srcDir, "./src"),
    },
  },
  server: {
    watch: {
      ignored: ["**/src-tauri/**"],
    },
    proxy: {
      "/drift.v1.": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
      },
    },
  },
});
