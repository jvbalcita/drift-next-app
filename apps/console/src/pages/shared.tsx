import type { ReactNode } from "react"
import { AlertTriangle, Check, ChevronLeft, ChevronRight, CircleHelp } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import type { DeviceView } from "@/lib/domain/control-plane"
import { deviceStatusLabels } from "@/lib/device-status"

export type StatusTone = "healthy" | "attention" | "neutral" | "danger" | "info"

const toneClasses: Record<StatusTone, string> = {
  healthy: "border-emerald-200 bg-emerald-50 text-emerald-800",
  attention: "border-amber-200 bg-amber-50 text-amber-800",
  neutral: "border-border bg-muted text-muted-foreground",
  danger: "border-red-200 bg-red-50 text-red-800",
  info: "border-primary/30 bg-secondary text-primary",
}

export function PageIntro({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow: string
  title: string
  description: string
  actions?: ReactNode
}) {
  return (
    <div className="mb-8 flex flex-col justify-between gap-5 md:flex-row md:items-end">
      <div>
        <div className="drift-kicker flex items-center gap-3">
          <span className="h-px w-8 bg-primary" aria-hidden="true" />
          <span>{eyebrow}</span>
        </div>
        <h1 className="mt-3 text-3xl font-semibold tracking-[-0.05em] sm:text-5xl">{title}</h1>
        <p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">{description}</p>
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  )
}

export function OperatorNotice({ children }: { children: ReactNode }) {
  return (
    <div role="note" className="mb-6 flex items-start gap-2 border-l-2 border-primary bg-secondary/70 p-3 text-xs leading-5 text-muted-foreground">
      <CircleHelp className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />
      <span><strong className="font-semibold text-foreground">Control Plane.</strong> {children}</span>
    </div>
  )
}

export function Panel({
  title,
  description,
  action,
  children,
  className = "",
}: {
  title: string
  description?: string
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <Card className={`rounded-none border border-border bg-card ${className}`}>
      <CardHeader className="border-b border-border pb-4">
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle className="text-sm font-semibold tracking-[-0.01em]">{title}</CardTitle>
            {description ? <CardDescription className="mt-2 text-xs leading-5">{description}</CardDescription> : null}
          </div>
          {action ? <div className="shrink-0">{action}</div> : null}
        </div>
      </CardHeader>
      <CardContent className="pt-4">{children}</CardContent>
    </Card>
  )
}

export function StatusBadge({ label, tone = "neutral" }: { label: string; tone?: StatusTone }) {
  return <Badge variant="outline" className={`rounded-none text-[10px] tracking-[0.04em] ${toneClasses[tone]}`}>{toTitleCase(label)}</Badge>
}

function toTitleCase(value: string) {
  return value.replaceAll("_", " ").split(" ").filter(Boolean).map((word) => word[0].toUpperCase() + word.slice(1)).join(" ")
}

export function FailureBadge({ failureClass }: { failureClass?: string }) {
  if (!failureClass) return null
  return <StatusBadge label={failureClass.replaceAll("_", " ")} tone="danger" />
}

export function FieldLabel({ htmlFor, children }: { htmlFor: string; children: ReactNode }) {
  return <label htmlFor={htmlFor} className="text-xs font-semibold text-foreground">{children}</label>
}

export type FormSelectOption = { value: string; label: string; disabled?: boolean }

export function FormSelect({ id, value, options, onValueChange, ariaLabel, disabled = false, size = "default", className = "" }: { id?: string; value: string; options: readonly FormSelectOption[]; onValueChange: (value: string) => void; ariaLabel?: string; disabled?: boolean; size?: "sm" | "default"; className?: string }) {
  return <Select items={options} value={value} onValueChange={(next) => { if (next !== null) onValueChange(next) }} disabled={disabled}>
    <SelectTrigger id={id} aria-label={ariaLabel} size={size} className={`rounded-none bg-background text-xs ${className}`}><SelectValue /></SelectTrigger>
    <SelectContent align="start" className="rounded-none">{options.map((option) => <SelectItem key={option.value} value={option.value} disabled={option.disabled} className="rounded-none text-xs">{option.label}</SelectItem>)}</SelectContent>
  </Select>
}

export function EmptyState({ label, detail }: { label: string; detail: string }) {
  return <div className="border border-dashed border-border bg-muted/40 px-4 py-8 text-center"><p className="text-sm font-medium">{label}</p><p className="mt-1 text-xs text-muted-foreground">{detail}</p></div>
}

export function DataTablePagination({
  page,
  pageSize,
  total,
  onPageChange,
  onPageSizeChange,
}: {
  page: number
  pageSize: number
  total: number
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
}) {
  const pageCount = Math.max(1, Math.ceil(total / pageSize))
  const start = total === 0 ? 0 : page * pageSize + 1
  const end = Math.min((page + 1) * pageSize, total)
  return (
    <div className="flex flex-wrap items-center gap-3 border-x border-b border-border bg-muted/30 px-3 py-2 text-xs">
      <p className="mr-auto text-muted-foreground" aria-live="polite">Showing {start}–{end} of {total} results</p>
      <div className="flex items-center gap-2"><span>Rows per page</span><FormSelect ariaLabel="Rows per page" value={String(pageSize)} onValueChange={(next) => onPageSizeChange(Number(next))} options={[5, 10, 25, 50].map((size) => ({ value: String(size), label: String(size) }))} className="w-16" /></div>
      <span className="drift-data text-[10px]">Page {page + 1} of {pageCount}</span>
      <Button size="icon-sm" variant="outline" aria-label="Previous page" disabled={page === 0} onClick={() => onPageChange(page - 1)}><ChevronLeft className="size-3.5" aria-hidden="true" /></Button>
      <Button size="icon-sm" variant="outline" aria-label="Next page" disabled={page >= pageCount - 1} onClick={() => onPageChange(page + 1)}><ChevronRight className="size-3.5" aria-hidden="true" /></Button>
    </div>
  )
}

export function DeviceIdentity({ device }: { device: DeviceView }) {
  return (
    <div className="min-w-0">
      <p className="truncate text-sm font-semibold">{device.displayName}</p>
      <p className="drift-data mt-1 truncate text-[10px] text-muted-foreground">stable · {device.stableIdentity}</p>
    </div>
  )
}

export function DeviceStatus({ device }: { device: DeviceView }) {
  const tone: StatusTone = device.status === "online" ? "healthy" : device.status === "attention" ? "attention" : "neutral"
  return <StatusBadge label={deviceStatusLabels[device.status]} tone={tone} />
}

export function OutcomeIcon({ ok }: { ok: boolean }) {
  return ok
    ? <Check className="size-3.5 text-emerald-700" aria-label="Passed" />
    : <AlertTriangle className="size-3.5 text-amber-700" aria-label="Needs review" />
}
