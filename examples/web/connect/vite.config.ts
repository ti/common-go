import { defineConfig } from "vite";

// https://vite.dev/config/
export default defineConfig({
  server: {
    port: 5173,
    // Same-origin in development: forward Connect calls to the Go server.
    proxy: {
      "/example.user.v1.UserService": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
  build: {
    target: "esnext",
    outDir: "dist",
  },
});
