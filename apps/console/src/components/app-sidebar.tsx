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
  onSectionChange,
  ...props
}: React.ComponentProps<typeof Sidebar> & {
  activeSection?: string
  onSectionChange?: (section: string) => void
}) {
  const navMain = [
    {
      title: "Overview",
      url: "#overview",
      icon: <LayoutDashboardIcon aria-hidden="true" />,
      isActive: activeSection === "Overview",
      items: [{ title: "Fleet overview", url: "#overview" }],
    },
    {
      title: "Control",
      url: "#control",
      icon: <MousePointer2Icon aria-hidden="true" />,
      isActive: activeSection === "Control",
      items: [{ title: "Mirror preview", url: "#control" }],
    },
    {
      title: "Devices",
      url: "#devices",
      icon: <SmartphoneIcon aria-hidden="true" />,
      isActive: activeSection === "Devices",
      badge: 6,
      items: [
        { title: "All devices", url: "#devices" },
        { title: "Needs attention", url: "#attention" },
      ],
    },
    {
      title: "Accounts",
      url: "#accounts",
      icon: <ContactRoundIcon aria-hidden="true" />,
      isActive: activeSection === "Accounts",
      items: [{ title: "References and assignments", url: "#accounts" }],
    },
    {
      title: "Network Profiles",
      url: "#network-profiles",
      icon: <NetworkIcon aria-hidden="true" />,
      isActive: activeSection === "Network Profiles",
      items: [{ title: "Discovery policy", url: "#network-profiles" }],
    },
    {
      title: "Groups",
      url: "#groups",
      icon: <UsersRoundIcon aria-hidden="true" />,
      isActive: activeSection === "Groups",
      items: [{ title: "Membership order", url: "#groups" }],
    },
    {
      title: "Workflows",
      url: "#workflows",
      icon: <CommandIcon aria-hidden="true" />,
      isActive: activeSection === "Workflows",
      items: [
        { title: "Definitions", url: "#workflows" },
        { title: "Schedules", url: "#schedules" },
      ],
    },
    {
      title: "Agents",
      url: "#agents",
      icon: <BotIcon aria-hidden="true" />,
      isActive: activeSection === "Agents",
      items: [{ title: "Profiles and assignments", url: "#agents" }],
    },
    {
      title: "Runs",
      url: "#runs",
      icon: <ListChecksIcon aria-hidden="true" />,
      isActive: activeSection === "Runs",
      items: [
        { title: "Active runs", url: "#runs" },
        { title: "History", url: "#history" },
      ],
    },
    {
      title: "Events",
      url: "#events",
      icon: <ScrollTextIcon aria-hidden="true" />,
      isActive: activeSection === "Events",
      items: [{ title: "Audit timeline", url: "#events" }],
    },
    {
      title: "Policies",
      url: "#policies",
      icon: <ShieldCheckIcon aria-hidden="true" />,
      isActive: activeSection === "Policies",
      items: [
        { title: "Safety policies", url: "#policies" },
        { title: "Access", url: "#access" },
      ],
    },
    {
      title: "Settings",
      url: "#settings",
      icon: <Settings2Icon aria-hidden="true" />,
      isActive: activeSection === "Settings",
      ariaLabel: "Configuration navigation",
      items: [{ title: "Scoped settings", url: "#settings" }],
    },
  ]

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
