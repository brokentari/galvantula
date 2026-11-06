import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev, the Go server (factory serve) answers /api.
export default defineConfig({
  plugins: [react()],
  server: { proxy: { "/api": "http://localhost:7070" } },
});
