import { MinusIcon, SquareIcon, XIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { SidebarTrigger } from "@/components/ui/sidebar"

type DesktopPlatform = "macos" | "windows" | "other"

function desktopPlatform(userAgent = navigator.userAgent): DesktopPlatform {
  if (/Macintosh|Mac OS X/i.test(userAgent)) return "macos"
  if (/Windows/i.test(userAgent)) return "windows"
  return "other"
}

async function runWindowAction(action: "minimize" | "maximize" | "close") {
  const { getCurrentWindow } = await import("@tauri-apps/api/window")
  const appWindow = getCurrentWindow()

  if (action === "minimize") await appWindow.minimize()
  if (action === "maximize") await appWindow.toggleMaximize()
  if (action === "close") await appWindow.close()
}

export function AppTitlebar({ platform = desktopPlatform() }: { platform?: DesktopPlatform }) {
  return (
    <div
      data-slot="app-titlebar"
      data-platform={platform}
      data-tauri-drag-region
      className="fixed inset-x-0 top-0 z-50 flex h-9 select-none items-center border-b border-white/8 bg-[#1f1f1f] text-white"
      aria-label="Window toolbar"
    >
      <SidebarTrigger
        data-platform={platform}
        aria-label="Toggle sidebar"
        className="absolute left-2 size-7 rounded-md text-zinc-300 hover:bg-white/10 hover:text-white focus-visible:ring-white/60 data-[platform=macos]:left-24 data-[platform=windows]:left-auto data-[platform=windows]:right-[146px]"
      />
      {platform === "windows" ? (
        <div className="ml-auto flex h-full" aria-label="Window controls">
          <Button
            variant="ghost"
            size="icon"
            className="h-full w-12 rounded-none text-zinc-300 hover:bg-white/10 hover:text-white"
            aria-label="Minimize window"
            onClick={() => void runWindowAction("minimize")}
          >
            <MinusIcon className="size-3.5" aria-hidden="true" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-full w-12 rounded-none text-zinc-300 hover:bg-white/10 hover:text-white"
            aria-label="Maximize window"
            onClick={() => void runWindowAction("maximize")}
          >
            <SquareIcon className="size-3" aria-hidden="true" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-full w-12 rounded-none text-zinc-300 hover:bg-red-600 hover:text-white"
            aria-label="Close window"
            onClick={() => void runWindowAction("close")}
          >
            <XIcon className="size-3.5" aria-hidden="true" />
          </Button>
        </div>
      ) : null}
    </div>
  )
}
