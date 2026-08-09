import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const rootEnv = loadEnv(mode, "..", "");
  const target =
    env.VITE_GATEWAY_PROXY_TARGET ??
    rootEnv.VITE_GATEWAY_PROXY_TARGET ??
    `http://127.0.0.1:${rootEnv.GATEWAY_HTTP_PORT || "8080"}`;
  const gateway = { changeOrigin: true, target };

  return {
    plugins: [react(), tailwindcss()],
    build: {
      chunkSizeWarningLimit: 600,
      rollupOptions: {
        output: {
          manualChunks(id) {
            if (!id.includes("node_modules")) return undefined;
            if (
              id.includes("/react/") ||
              id.includes("/react-dom/") ||
              id.includes("/react-router")
            ) {
              return "react";
            }
            return undefined;
          },
        },
      },
    },
    server: {
      port: 4173,
      proxy: {
        // 管理 API
        "/api": gateway,
        // OCI Registry V2 协议端点（镜像详情、推送）
        "/v2": gateway,
        "/auth": gateway,
        // 其他协议端点
        "/raw": gateway,
        "/repository": gateway,
        "/maven": gateway,
        "/conan": gateway,
        "/npm": gateway,
        "/pypi": gateway,
        "/go": gateway,
      },
    },
  };
});
