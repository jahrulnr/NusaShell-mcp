import { useState, useEffect, useCallback } from "react";

export type ScrollMode = "column" | "page";

export function useScrollMode() {
  const [scrollMode, setScrollModeState] = useState<ScrollMode>(() => {
    const stored = localStorage.getItem("mcp-kanban-scroll-mode") as ScrollMode | null;
    return stored === "page" ? "page" : "column";
  });

  const setScrollMode = useCallback((mode: ScrollMode) => {
    setScrollModeState(mode);
    localStorage.setItem("mcp-kanban-scroll-mode", mode);
  }, []);

  return { scrollMode, setScrollMode };
}
