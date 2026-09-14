"use client"

import * as React from "react"

import { NavMain } from "@/components/nav-main"
import { NavProjects } from "@/components/nav-projects"
import { NavUser } from "@/components/nav-user"
import { TeamSwitcher } from "@/components/team-switcher"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarRail,
} from "@/components/ui/sidebar"
import {
  ActivityIcon,
  ContactRoundIcon,
  BotIcon,
  CommandIcon,
  LayoutDashboardIcon,
  ListChecksIcon,
  MousePointer2Icon,
  NetworkIcon,
  ScrollTextIcon,
  ServerIcon,
  ShieldCheckIcon,
  SmartphoneIcon,
  Settings2Icon,
  UsersRoundIcon,
} from "lucide-react"
import { navigation, type Section } from "@/lib/navigation"

const teams = [
  {
    name: "DRIFT",
    logo: <CommandIcon className="size-4" aria-hidden="true" />,
    plan: "Demo control plane",
  },
  {
    name: "Fleet Ops",
    logo: <ServerIcon className="size-4" aria-hidden="true" />,
    plan: "Read-only workspace",
  },
  {
    name: "Automation Lab",
    logo: <ActivityIcon className="size-4" aria-hidden="true" />,
    plan: "Mock environment",
  },
]

const projects = [
  {
    name: "Rack A",
    url: "#rack-a",
    icon: <ServerIcon aria-hidden="true" />,
  },
  {
    name: "Rack B",
    url: "#rack-b",
    icon: <ServerIcon aria-hidden="true" />,
  },
  {
    name: "Rack C",
    url: "#rack-c",
    icon: <ServerIcon aria-hidden="true" />,
  },
]

const user = {
  name: "Drift Operator",
  email: "Demo session",
  avatar: "",
}

export function AppSidebar({
  activeSection = "Overview",
  activeView,
  onSectionChange,
  ...props
}: React.ComponentProps<typeof Sidebar> & {
  activeSection?: Section
  activeView?: string
  onSectionChange?: (section: string, view?: string) => void
}) {
  const iconFor: Record<Section, React.ReactNode> = { Overview: <LayoutDashboardIcon aria-hidden="true" />, Control: <MousePointer2Icon aria-hidden="true" />, Devices: <SmartphoneIcon aria-hidden="true" />, Accounts: <ContactRoundIcon aria-hidden="true" />, "Network Profiles": <NetworkIcon aria-hidden="true" />, Groups: <UsersRoundIcon aria-hidden="true" />, Workflows: <CommandIcon aria-hidden="true" />, Agents: <BotIcon aria-hidden="true" />, Runs: <ListChecksIcon aria-hidden="true" />, Events: <ScrollTextIcon aria-hidden="true" />, Policies: <ShieldCheckIcon aria-hidden="true" />, Settings: <Settings2Icon aria-hidden="true" /> }
  const navMain = navigation.map((item) => ({
    title: item.section,
    url: `#${item.hash}/${item.views[0].id}`,
    icon: iconFor[item.section],
    isActive: activeSection === item.section,
    badge: item.section === "Devices" ? 6 : undefined,
    ariaLabel: item.section === "Settings" ? "Configuration navigation" : undefined,
    items: item.views.map((view) => ({ title: view.label, url: `#${item.hash}/${view.id}`, isActive: activeSection === item.section && activeView === view.id })),
  }))

  return (
    <Sidebar collapsible="icon" className="border-sidebar-border/70" {...props}>
      <SidebarHeader>
        <TeamSwitcher teams={teams} />
      </SidebarHeader>
      <SidebarContent>
        <nav aria-label="Primary navigation">
          <NavMain items={navMain} label="Workspace" onSelect={onSectionChange} />
        </nav>
        <NavProjects projects={projects} label="Racks" />
      </SidebarContent>
      <SidebarFooter>
        <NavUser user={user} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
