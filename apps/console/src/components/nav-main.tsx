import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"
import type { MouseEvent } from "react"

export function NavMain({
  groups,
  onSelect,
}: {
  groups: {
    label: string
    items: {
      title: string
      url: string
      view: string
      icon?: React.ReactNode
      isActive?: boolean
    }[]
  }[]
  onSelect?: (section: string, view?: string) => void
}) {
  const { isMobile, setOpenMobile } = useSidebar()

  function select(event: MouseEvent<HTMLAnchorElement>, section: string, view: string) {
    if (!onSelect || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    onSelect?.(section, view)
    if (isMobile) setOpenMobile(false)
  }

  return (
    <>
      {groups.map((group) => (
        <SidebarGroup key={group.label} className="py-2">
          <SidebarGroupLabel className="text-[10px] font-semibold tracking-[0.16em] uppercase">
            {group.label}
          </SidebarGroupLabel>
          <SidebarMenu>
            {group.items.map((item) => (
              <SidebarMenuItem key={item.title}>
                <SidebarMenuButton
                  className="rounded-none border-l-2 border-transparent data-active:border-sidebar-primary"
                  tooltip={item.title}
                  isActive={item.isActive}
                  render={
                    <a
                      href={item.url}
                      aria-current={item.isActive ? "page" : undefined}
                      onClick={(event) => select(event, item.title, item.view)}
                    />
                  }
                >
                  {item.icon}
                  <span>{item.title}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
      ))}
    </>
  )
}
