import { BellIcon, MinusIcon, Settings2Icon, SquareIcon, XIcon } from "lucide-react"

import { Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator } from "@/components/ui/breadcrumb"
import { Button } from "@/components/ui/button"
import { SidebarTrigger, useSidebar } from "@/components/ui/sidebar"
import { viewLabel, type Route } from "@/lib/navigation"
import { cn } from "@/lib/utils"

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

export function AppTitlebar({
  route,
  onOpenSettings,
  platform = desktopPlatform(),
}: {
  route: Route
  onOpenSettings?: () => void
  platform?: DesktopPlatform
}) {
  const { state } = useSidebar()
  const sidebarOpen = state === "expanded"

  return (
    <header
      data-slot="app-titlebar"
      data-platform={platform}
      data-sidebar-state={state}
      data-tauri-drag-region
      className="fixed inset-x-0 top-0 z-50 flex h-10 select-none items-center border-b border-border bg-background text-foreground"
      aria-label="Window toolbar"
    >
      <div
        data-slot="titlebar-sidebar-surface"
        data-tauri-drag-region
        className={cn(
          "absolute inset-y-0 left-0 border-r border-sidebar-border bg-sidebar transition-[width] duration-200 ease-linear",
          sidebarOpen ? "w-(--sidebar-width)" : "w-0 border-r-0",
        )}
      />

      <SidebarTrigger
        data-platform={platform}
        aria-label="Toggle sidebar"
        className="absolute top-1/2 left-2 size-7 -translate-y-1/2 rounded-md text-muted-foreground hover:bg-foreground/7 hover:text-foreground focus-visible:ring-ring data-[platform=macos]:left-[88px] data-[platform=windows]:left-auto data-[platform=windows]:right-[146px]"
      />

      <div
        data-slot="titlebar-content"
        data-tauri-drag-region
        className={cn(
          "flex min-w-0 flex-1 items-center transition-[margin] duration-200 ease-linear",
          sidebarOpen ? "ml-(--sidebar-width)" : platform === "macos" ? "ml-32" : "ml-12",
          platform === "windows" && "mr-36",
        )}
      >
        <Breadcrumb className="min-w-0 px-4">
          <BreadcrumbList className="flex-nowrap text-xs">
            <BreadcrumbItem className="hidden md:block"><BreadcrumbLink href="#workspace">Workspace</BreadcrumbLink></BreadcrumbItem>
            <BreadcrumbSeparator className="hidden md:block" />
            <BreadcrumbItem><BreadcrumbPage>{route.section === "Control" ? "Control Center" : route.section}</BreadcrumbPage></BreadcrumbItem>
            {route.section !== "Control" ? <><BreadcrumbSeparator className="hidden sm:block" /><BreadcrumbItem className="hidden min-w-0 sm:block"><BreadcrumbPage className="truncate">{viewLabel(route)}</BreadcrumbPage></BreadcrumbItem></> : null}
          </BreadcrumbList>
        </Breadcrumb>

        <div className="ml-auto flex shrink-0 items-center gap-1 px-3">
          <Button variant="ghost" size="icon-sm" aria-label="Notifications"><BellIcon className="size-4" aria-hidden="true" /></Button>
          <Button variant="ghost" size="icon-sm" aria-label="Settings" onClick={onOpenSettings}><Settings2Icon className="size-4" aria-hidden="true" /></Button>
        </div>
      </div>

      {platform === "windows" ? (
        <div className="absolute inset-y-0 right-0 flex" aria-label="Window controls">
          <Button variant="ghost" size="icon" className="h-full w-12 rounded-none text-muted-foreground hover:bg-foreground/7 hover:text-foreground" aria-label="Minimize window" onClick={() => void runWindowAction("minimize")}><MinusIcon className="size-3.5" aria-hidden="true" /></Button>
          <Button variant="ghost" size="icon" className="h-full w-12 rounded-none text-muted-foreground hover:bg-foreground/7 hover:text-foreground" aria-label="Maximize window" onClick={() => void runWindowAction("maximize")}><SquareIcon className="size-3" aria-hidden="true" /></Button>
          <Button variant="ghost" size="icon" className="h-full w-12 rounded-none text-muted-foreground hover:bg-red-600 hover:text-white" aria-label="Close window" onClick={() => void runWindowAction("close")}><XIcon className="size-3.5" aria-hidden="true" /></Button>
        </div>
      ) : null}
    </header>
  )
}
