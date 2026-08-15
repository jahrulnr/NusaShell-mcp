import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

export default defineConfig({
  base: "./",
  plugins: [react(), tailwindcss()],
  root: "ui-src",
  resolve: { alias: { "@shared": path.resolve(__dirname, "shared"), "@": path.resolve(__dirname, "ui-src") } },
  build: { outDir: path.resolve(__dirname, "ui"), emptyOutDir: true },
});
