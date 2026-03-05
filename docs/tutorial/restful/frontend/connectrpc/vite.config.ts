import { defineConfig } from "vite";

// https://vitejs.dev/config/
export default defineConfig({
  // Development server: proxy /pb.UserService/* to the Go backend.
  // This avoids CORS issues during local development.
  // The backend listens on :8080 in h2c mode by default.
  server: {
    port: 5173,
    proxy: {
      // Proxy ConnectRPC requests to Go backend (h2c mode)
      "/pb.UserService": {
        target: "http://localhost:8080",
        changeOrigin: true,
        // Required for h2c: disable SSL verification (not applicable here, but good practice)
        secure: false,
      },
    },
  },
  build: {
    target: "esnext",
    outDir: "dist",
  },
});
