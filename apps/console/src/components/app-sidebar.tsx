"use client"

import * as React from "react"

import { NavMain } from "@/components/nav-main"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  ArchiveIcon,
  ContactRoundIcon,
  BotIcon,
  CommandIcon,
  LayoutDashboardIcon,
  ListChecksIcon,
  MousePointer2Icon,
  NetworkIcon,
  ScrollTextIcon,
  ShieldCheckIcon,
  SmartphoneIcon,
  Settings2Icon,
  UsersRoundIcon,
  ChevronsUpDownIcon,
} from "lucide-react"
import { navigation, navigationGroups, type Section } from "@/lib/navigation"

export function AppSidebar({
  activeSection = "Overview",
  onSectionChange,
  ...props
}: React.ComponentProps<typeof Sidebar> & {
  activeSection?: Section
  onSectionChange?: (section: string, view?: string) => void
}) {
  const { isMobile } = useSidebar()
  const iconFor: Record<Section, React.ReactNode> = { Overview: <LayoutDashboardIcon aria-hidden="true" />, Control: <MousePointer2Icon aria-hidden="true" />, Devices: <SmartphoneIcon aria-hidden="true" />, Accounts: <ContactRoundIcon aria-hidden="true" />, "Network Profiles": <NetworkIcon aria-hidden="true" />, Groups: <UsersRoundIcon aria-hidden="true" />, Workflows: <CommandIcon aria-hidden="true" />, Agents: <BotIcon aria-hidden="true" />, Runs: <ListChecksIcon aria-hidden="true" />, Artifacts: <ArchiveIcon aria-hidden="true" />, Events: <ScrollTextIcon aria-hidden="true" />, Policies: <ShieldCheckIcon aria-hidden="true" />, Settings: <Settings2Icon aria-hidden="true" /> }
  const navGroups = navigationGroups.map((label) => ({
    label,
    items: navigation.filter((item) => item.group === label).map((item) => ({
      title: item.section,
      url: `#${item.hash}/${item.views[0].id}`,
      view: item.views[0].id,
      icon: iconFor[item.section],
      isActive: activeSection === item.section,
    })),
  }))

  return (
    <Sidebar collapsible="icon" className="border-sidebar-border/70" {...props}>
      <SidebarHeader>
        <div className="flex h-12 items-center gap-3 border-b border-sidebar-border px-2 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0">
          <span className="flex size-8 shrink-0 items-center justify-center border border-sidebar-border bg-sidebar-primary text-sidebar-primary-foreground">
            <CommandIcon className="size-4" aria-hidden="true" />
          </span>
          <span className="min-w-0 group-data-[collapsible=icon]:hidden">
            <span className="block truncate text-xs font-bold tracking-[0.18em]">DRIFT</span>
            <span className="block truncate text-[10px] text-muted-foreground">Local Control Plane</span>
          </span>
        </div>
      </SidebarHeader>
      <SidebarContent>
        <nav aria-label="Primary navigation">
          <NavMain groups={navGroups} onSelect={onSectionChange} />
        </nav>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <SidebarMenuButton
                    size="lg"
                    tooltip="Drift Operator"
                    className="h-12 rounded-none border-t border-sidebar-border px-2 data-popup-open:bg-sidebar-accent"
                  />
                }
              >
                <span className="relative flex size-8 shrink-0 items-center justify-center border border-sidebar-border text-xs font-semibold" aria-hidden="true">
                  DO
                  <span className="absolute -right-0.5 -bottom-0.5 size-2 border border-sidebar bg-emerald-600" />
                </span>
                <span className="min-w-0 flex-1 text-left group-data-[collapsible=icon]:hidden">
                  <span className="block truncate text-xs font-semibold">Drift Operator</span>
                  <span className="block truncate text-[10px] text-muted-foreground">Operator Session</span>
                </span>
                <ChevronsUpDownIcon className="ml-auto size-3.5 group-data-[collapsible=icon]:hidden" aria-hidden="true" />
              </DropdownMenuTrigger>
              <DropdownMenuContent
                className="min-w-60 rounded-none border border-border shadow-none"
                side={isMobile ? "top" : "right"}
                align="end"
                sideOffset={8}
              >
                <DropdownMenuGroup>
                  <DropdownMenuLabel className="px-2 py-2 font-normal">
                    <span className="block text-xs font-semibold text-foreground">Drift Operator</span>
                    <span className="mt-0.5 block text-[10px] text-muted-foreground">Local operator session · Online</span>
                  </DropdownMenuLabel>
                </DropdownMenuGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem className="rounded-none px-2 py-2" onClick={() => onSectionChange?.("Overview", "fleet")}>
                  <LayoutDashboardIcon aria-hidden="true" />
                  Fleet Overview
                </DropdownMenuItem>
                <DropdownMenuItem className="rounded-none px-2 py-2" onClick={() => onSectionChange?.("Settings", "workspace")}>
                  <Settings2Icon aria-hidden="true" />
                  Operator Settings
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
