import type { ReactNode } from "react"
import { AlertTriangle, Check, CircleHelp } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import type { DeviceView } from "@/lib/domain/control-plane"

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

export function MockNotice({ children }: { children: ReactNode }) {
  return (
    <div role="note" className="mb-6 flex items-start gap-2 border-l-2 border-primary bg-secondary/70 p-3 text-xs leading-5 text-muted-foreground">
      <CircleHelp className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />
      <span><strong className="font-semibold text-foreground">Mock control plane.</strong> {children}</span>
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
            <CardTitle className="text-sm font-semibold uppercase tracking-[0.08em]">{title}</CardTitle>
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
  return <Badge variant="outline" className={`rounded-none text-[10px] uppercase tracking-[0.06em] ${toneClasses[tone]}`}>{label}</Badge>
}

export function FailureBadge({ failureClass }: { failureClass?: string }) {
  if (!failureClass) return null
  return <StatusBadge label={failureClass.replaceAll("_", " ")} tone="danger" />
}

export function FieldLabel({ htmlFor, children }: { htmlFor: string; children: ReactNode }) {
  return <label htmlFor={htmlFor} className="font-mono text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">{children}</label>
}

export function EmptyState({ label, detail }: { label: string; detail: string }) {
  return <div className="border border-dashed border-border bg-muted/40 px-4 py-8 text-center"><p className="text-sm font-medium">{label}</p><p className="mt-1 text-xs text-muted-foreground">{detail}</p></div>
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
  const label = device.status === "online" ? "Online" : device.status === "attention" ? "Attention" : "Offline"
  return <StatusBadge label={label} tone={tone} />
}

export function OutcomeIcon({ ok }: { ok: boolean }) {
  return ok
    ? <Check className="size-3.5 text-emerald-700" aria-label="Passed" />
    : <AlertTriangle className="size-3.5 text-amber-700" aria-label="Needs review" />
}
