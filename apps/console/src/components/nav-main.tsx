import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
} from "@/components/ui/sidebar"
import { ChevronRightIcon } from "lucide-react"

export function NavMain({
  items,
  label = "Platform",
  onSelect,
}: {
  items: {
    title: string
    url: string
    icon?: React.ReactNode
    isActive?: boolean
    ariaLabel?: string
    badge?: string | number
    items?: {
      title: string
      url: string
    }[]
  }[]
  label?: string
  onSelect?: (section: string) => void
}) {
  return (
    <SidebarGroup>
      <SidebarGroupLabel>{label}</SidebarGroupLabel>
      <SidebarMenu>
        {items.map((item) => (
          <Collapsible
            key={item.title}
            defaultOpen={item.isActive}
            className="group/collapsible"
            render={<SidebarMenuItem />}
          >
            <CollapsibleTrigger
              render={
                <SidebarMenuButton
                  tooltip={item.title}
                  isActive={item.isActive}
                  aria-label={item.ariaLabel}
                  aria-current={item.isActive ? "page" : undefined}
                  onClick={() => onSelect?.(item.title)}
                />
              }
            >
              {item.icon}
              <span>{item.title}</span>
              <ChevronRightIcon className="ml-auto transition-transform duration-200 group-data-open/collapsible:rotate-90" aria-hidden="true" />
            </CollapsibleTrigger>
            {item.badge !== undefined ? <SidebarMenuBadge className="right-7">{item.badge}</SidebarMenuBadge> : null}
            <CollapsibleContent>
              <SidebarMenuSub>
                {item.items?.map((subItem) => (
                  <SidebarMenuSubItem key={subItem.title}>
                    <SidebarMenuSubButton
                      render={
                        <a
                          href={subItem.url}
                          onClick={(event) => {
                            event.preventDefault()
                            onSelect?.(item.title)
                          }}
                        />
                      }
                    >
                      <span>{subItem.title}</span>
                    </SidebarMenuSubButton>
                  </SidebarMenuSubItem>
                ))}
              </SidebarMenuSub>
            </CollapsibleContent>
          </Collapsible>
        ))}
      </SidebarMenu>
    </SidebarGroup>
  )
}
