import type { VisualTheme, ColorMode } from "../../hooks/useTheme";
import type { ScrollMode } from "../../hooks/useScrollMode";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";

interface SettingsModalProps {
  open: boolean;
  onClose: () => void;
  visualTheme: VisualTheme;
  colorMode: ColorMode;
  setVisualTheme: (value: VisualTheme) => void;
  setColorMode: (value: ColorMode) => void;
  scrollMode: ScrollMode;
  setScrollMode: (value: ScrollMode) => void;
}

export function SettingsModal({ open, onClose, visualTheme, colorMode, setVisualTheme, setColorMode, scrollMode, setScrollMode }: SettingsModalProps) {
  return <Dialog open={open} onOpenChange={(value) => !value && onClose()}>
    <DialogContent className="sm:max-w-md">
      <DialogHeader><DialogTitle>Board settings</DialogTitle></DialogHeader>
      <div className="space-y-5">
        <div><Label>Visual theme</Label><select className="mt-2 w-full rounded-md border bg-background p-2" value={visualTheme} onChange={(event) => setVisualTheme(event.target.value as VisualTheme)}><option value="default">Default</option><option value="brutalist">Brutalist</option><option value="glass">Glass</option><option value="softnight">Softnight</option></select></div>
        <div><Label>Color mode</Label><select className="mt-2 w-full rounded-md border bg-background p-2" value={colorMode} onChange={(event) => setColorMode(event.target.value as ColorMode)}><option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option></select></div>
        <div><Label>Board scrolling</Label><select className="mt-2 w-full rounded-md border bg-background p-2" value={scrollMode} onChange={(event) => setScrollMode(event.target.value as ScrollMode)}><option value="column">Scroll columns</option><option value="page">Scroll page</option></select></div>
        <p className="text-xs text-muted-foreground">Projects, tickets, and sessions are managed through the Kanban tools.</p>
      </div>
    </DialogContent>
  </Dialog>;
}
