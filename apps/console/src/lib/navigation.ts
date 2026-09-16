export const navigation = [
  { section: "Overview", hash: "overview", views: [{ id: "fleet", label: "Fleet Overview" }] },
  { section: "Control", hash: "control", views: [{ id: "workspace", label: "Control Center" }] },
  { section: "Devices", hash: "devices", views: [{ id: "all", label: "All" }, { id: "online", label: "Online" }, { id: "attention", label: "Needs Attention" }, { id: "replaced", label: "Replaced" }, { id: "retired", label: "Retired" }] },
  { section: "Accounts", hash: "accounts", views: [{ id: "sources", label: "Sources" }, { id: "accounts", label: "Accounts" }, { id: "assignments", label: "Device Assignments" }, { id: "service-history", label: "Service History" }, { id: "run-history", label: "Run History" }] },
  { section: "Network Profiles", hash: "network-profiles", views: [{ id: "profiles", label: "Profiles" }, { id: "scans", label: "Discovery Scans" }, { id: "endpoints", label: "Registered Endpoints" }] },
  { section: "Groups", hash: "groups", views: [{ id: "groups", label: "Groups" }] },
  { section: "Workflows", hash: "workflows", views: [{ id: "definitions", label: "Definitions" }, { id: "versions", label: "Versions" }, { id: "skills", label: "Skills" }] },
  { section: "Agents", hash: "agents", views: [{ id: "runtimes", label: "Edge Runtimes" }, { id: "profiles", label: "Logical Profiles" }, { id: "assignments", label: "Assignments" }, { id: "capabilities", label: "Capabilities" }] },
  { section: "Runs", hash: "runs", views: [{ id: "active", label: "Active" }, { id: "history", label: "History" }, { id: "failed", label: "Failed / Indeterminate" }] },
  { section: "Artifacts", hash: "artifacts", views: [{ id: "library", label: "Library" }, { id: "media", label: "Media" }, { id: "recordings", label: "Recordings" }, { id: "storage", label: "Storage" }, { id: "audit", label: "Audit" }] },
  { section: "Events", hash: "events", views: [{ id: "all", label: "Event Log" }] },
  { section: "Policies", hash: "policies", views: [{ id: "active", label: "Active Policies" }, { id: "versions", label: "Versions" }, { id: "decisions", label: "Decision Log" }, { id: "access", label: "Access" }] },
  { section: "Settings", hash: "settings", views: [{ id: "workspace", label: "Workspace" }, { id: "control-plane", label: "Control Plane" }, { id: "edge-host", label: "Edge Host" }, { id: "device", label: "Device" }, { id: "automation-agent", label: "Automation Agent" }, { id: "operator-preference", label: "Operator Preference" }, { id: "history", label: "History" }] },
] as const

export type Section = (typeof navigation)[number]["section"]
export type Route = { section: Section; view: string; recordId?: string }

export function routeFromHash(hash: string): Route {
  const [rawSection = "overview", rawView, recordId] = hash.replace(/^#/, "").split("/")
  const item = navigation.find((candidate) => candidate.hash === rawSection) ?? navigation[0]
  const view = item.views.find((candidate) => candidate.id === rawView)?.id ?? item.views[0].id
  return { section: item.section, view, recordId }
}

export function hashForRoute(route: Route) {
  const item = navigation.find((candidate) => candidate.section === route.section)
  return item ? `#${item.hash}/${route.view}${route.recordId ? `/${route.recordId}` : ""}` : "#overview/fleet"
}

export function viewLabel(route: Route) {
  return navigation.find((item) => item.section === route.section)?.views.find((view) => view.id === route.view)?.label ?? route.section
}
