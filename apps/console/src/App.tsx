import { lazy, Suspense, useEffect, useState } from "react"
import { AppSidebar } from "./components/app-sidebar"
import { AppTitlebar } from "./components/app-titlebar"
import { Button } from "./components/ui/button"
import {
  SidebarInset,
  SidebarProvider,
} from "./components/ui/sidebar"
import { TooltipProvider } from "./components/ui/tooltip"
import { Toaster } from "./components/ui/sonner"
import { Skeleton } from "./components/ui/skeleton"
import { useControlPlane } from "./lib/api/use-control-plane"
import { useGridPreviewClient, useLiveMirrorClient } from "./lib/api/live-mirror-client"
import type { GridPreviewClient, LiveMirrorClient } from "./lib/api/control-plane-clients"
import { hashForRoute, routeFromHash, type Route, type Section } from "./lib/navigation"
import { AgentsPage } from "./pages/AgentsPage"
import { ControlPage } from "./pages/ControlPage"
import { OverviewPage } from "./pages/OverviewPage"
const DevicesPage = lazy(async () => ({ default: (await import("./pages/DevicesPage")).DevicesPage }))
const AccountsPage = lazy(async () => ({ default: (await import("./pages/AccountsPage")).AccountsPage }))
const EventsPage = lazy(async () => ({ default: (await import("./pages/EventsPage")).EventsPage }))
const GroupsPage = lazy(async () => ({ default: (await import("./pages/GroupsPage")).GroupsPage }))
const NetworkProfilesPage = lazy(async () => ({ default: (await import("./pages/NetworkProfilesPage")).NetworkProfilesPage }))
const PoliciesPage = lazy(async () => ({ default: (await import("./pages/PoliciesPage")).PoliciesPage }))
const RunsPage = lazy(async () => ({ default: (await import("./pages/RunsPage")).RunsPage }))
const WorkflowsPage = lazy(async () => ({ default: (await import("./pages/WorkflowsPage")).WorkflowsPage }))
const SettingsPage = lazy(async () => ({ default: (await import("./pages/SettingsPage")).SettingsPage }))
const ArtifactsPage = lazy(async () => ({ default: (await import("./pages/ArtifactsPage")).ArtifactsPage }))

function App() {
  const [route, setRoute] = useState<Route>(() => routeFromHash(window.location.hash))
  const { snapshot, dispatch, dispatchLab, labNotice, loading, connectionError, reload } = useControlPlane()
  const liveMirror = useLiveMirrorClient()
  // The grid's stills are read from the same resolved control plane the big frame's
  // stream is opened on, so the pictures beside the frame cannot come from a second,
  // differently configured plane.
  const gridPreviews = useGridPreviewClient()
  useEffect(() => {
    const syncRoute = () => setRoute(routeFromHash(window.location.hash))
    window.addEventListener("hashchange", syncRoute)
    return () => window.removeEventListener("hashchange", syncRoute)
  }, [])
  function navigate(section: Section, view?: string) {
    const next = routeFromHash(hashForRoute({ section, view: view ?? "" }))
    setRoute(next)
    window.location.hash = hashForRoute(next)
  }

  return (
    <TooltipProvider>
      <SidebarProvider defaultOpen data-visual-style="swiss-editorial" className="drift-theme min-h-svh bg-background pt-10 text-foreground">
        <AppTitlebar route={route} onOpenSettings={() => navigate("Settings")} />
        <AppSidebar className="top-10 h-[calc(100svh-2.5rem)]" activeSection={route.section} onSectionChange={(section, view) => navigate(section as Section, view)} />
        <SidebarInset className="min-w-0">
          <main className="drift-editorial-grid mx-auto w-full max-w-[1800px] flex-1 p-4 sm:p-6 lg:p-8">
            {loading ? <p role="status" className="mb-4 border border-border bg-muted/30 p-3 text-xs text-muted-foreground">Loading Control Plane…</p> : null}
            {connectionError ? (
              <div role="alert" className="mb-4 flex flex-wrap items-center gap-3 border border-border bg-red-50 p-3 text-xs text-red-800">
                <span className="flex-1">{connectionError}</span>
                <Button size="sm" variant="outline" onClick={() => void reload()}>Retry</Button>
              </div>
            ) : null}
            <Suspense fallback={<ConsoleLoadingState />}>
              {renderSection(route, snapshot, dispatch, dispatchLab, labNotice, (view) => navigate(route.section, view), liveMirror, gridPreviews)}
            </Suspense>
          </main>
        </SidebarInset>
      </SidebarProvider>
      <Toaster position="bottom-right" />
    </TooltipProvider>
  )
}

function ConsoleLoadingState() {
  return <div role="status" className="space-y-4 border border-border bg-card p-6" aria-label="Loading console surface"><Skeleton className="h-4 w-32 rounded-none" /><Skeleton className="h-10 w-2/5 rounded-none" /><div className="grid gap-3 md:grid-cols-3"><Skeleton className="h-28 rounded-none" /><Skeleton className="h-28 rounded-none" /><Skeleton className="h-28 rounded-none" /></div></div>
}

function renderSection(route: Route, snapshot: ReturnType<typeof useControlPlane>["snapshot"], dispatch: ReturnType<typeof useControlPlane>["dispatch"], dispatchLab: ReturnType<typeof useControlPlane>["dispatchLab"], labNotice: string, onViewChange: (view: string) => void, liveMirror?: LiveMirrorClient, gridPreviews?: GridPreviewClient) {
  switch (route.section) {
    case "Control": return <ControlPage snapshot={snapshot} dispatch={dispatch} dispatchLab={dispatchLab} labNotice={labNotice} mirror={liveMirror} grid={gridPreviews} />
    case "Devices": return <DevicesPage snapshot={snapshot} dispatch={dispatch} view={route.view} onViewChange={onViewChange} />
    case "Accounts": return <AccountsPage snapshot={snapshot} dispatch={dispatch} view={route.view} onViewChange={onViewChange} />
    case "Network Profiles": return <NetworkProfilesPage snapshot={snapshot} dispatch={dispatch} view={route.view} onViewChange={onViewChange} />
    case "Groups": return <GroupsPage snapshot={snapshot} dispatch={dispatch} />
    case "Agents": return <AgentsPage snapshot={snapshot} dispatch={dispatch} view={route.view} onViewChange={onViewChange} />
    case "Workflows": return <WorkflowsPage snapshot={snapshot} dispatch={dispatch} view={route.view} onViewChange={onViewChange} />
    case "Runs": return <RunsPage snapshot={snapshot} dispatch={dispatch} view={route.view} onViewChange={onViewChange} />
    case "Artifacts": return <ArtifactsPage snapshot={snapshot} dispatch={dispatch} view={route.view} onViewChange={onViewChange} />
    case "Events": return <EventsPage snapshot={snapshot} />
    case "Policies": return <PoliciesPage snapshot={snapshot} dispatch={dispatch} view={route.view} onViewChange={onViewChange} />
    case "Settings": return <SettingsPage snapshot={snapshot} dispatch={dispatch} view={route.view} onViewChange={onViewChange} />
    case "Overview":
    default: return <OverviewPage snapshot={snapshot} dispatch={dispatch} />
  }
}

export default App
