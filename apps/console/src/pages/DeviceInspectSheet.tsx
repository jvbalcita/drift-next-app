import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { DeviceView } from "@/lib/domain/control-plane"
import { stableIdentityLabel, type InspectionSection, type InspectionTab } from "./device-inspection"

/**
 * DeviceInspectSheet renders the inspection surface for one device.
 *
 * The title is the device's stable identity: the transport address is a mutable
 * endpoint attribute and never names the device. Only the tabs the snapshot can
 * back with real projection data are rendered, and each rendered section holds
 * at least one value.
 */
export function DeviceInspectSheet({
  device,
  tabs,
  activeTab,
  onTabChange,
  onClose,
}: {
  device?: DeviceView
  tabs: readonly InspectionTab[]
  activeTab: string
  onTabChange: (tab: string) => void
  onClose: () => void
}) {
  return (
    <Sheet open={Boolean(device)} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent className="w-full rounded-none sm:max-w-xl">
        <SheetHeader className="border-b border-border">
          <SheetTitle className="drift-data break-all text-sm">{device ? stableIdentityLabel(device) : "Device Inspection"}</SheetTitle>
          <SheetDescription>Stable identity names this device. Transport endpoints are mutable projections and never title it.</SheetDescription>
        </SheetHeader>
        {device ? (
          <Tabs value={activeTab} onValueChange={(value) => onTabChange(String(value))} className="overflow-y-auto p-4">
            <TabsList className="h-auto flex-wrap rounded-none border border-border bg-background p-0" aria-label="Device Inspection Views">
              {tabs.map((tab) => <TabsTrigger key={tab.id} value={tab.id} className="rounded-none">{tab.label}</TabsTrigger>)}
            </TabsList>
            {tabs.map((tab) => (
              <TabsContent key={tab.id} value={tab.id} className="space-y-5">
                {tab.sections.map((section, index) => <InspectionSectionView key={`${tab.id}-${section.title ?? index}`} section={section} />)}
              </TabsContent>
            ))}
          </Tabs>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

function InspectionSectionView({ section }: { section: InspectionSection }) {
  return (
    <section className="space-y-3">
      {section.title ? <h3 className="text-[10px] font-semibold uppercase tracking-[.08em] text-muted-foreground">{section.title}</h3> : null}
      {section.description ? <p className="text-[11px] leading-5 text-muted-foreground">{section.description}</p> : null}
      {section.rows ? (
        <dl className="grid gap-3 text-xs sm:grid-cols-2">
          {section.rows.map((entry) => <InspectionField key={entry.label} label={entry.label} value={entry.value} mono={entry.mono} />)}
        </dl>
      ) : null}
      {section.items ? (
        <div className="space-y-3">
          {section.items.map((item) => (
            <dl key={item.key} className="grid gap-3 border border-border p-3 text-xs sm:grid-cols-2">
              {item.rows.map((entry) => <InspectionField key={entry.label} label={entry.label} value={entry.value} mono={entry.mono} />)}
            </dl>
          ))}
        </div>
      ) : null}
    </section>
  )
}

function InspectionField({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{label}</dt>
      <dd className={`mt-1 break-words ${mono ? "drift-data text-[11px]" : "text-xs"}`}>{value}</dd>
    </div>
  )
}
