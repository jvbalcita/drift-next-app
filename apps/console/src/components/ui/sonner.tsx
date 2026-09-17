import { Toaster as Sonner, type ToasterProps } from "sonner"
import { CircleCheckIcon, InfoIcon, Loader2Icon, OctagonXIcon, TriangleAlertIcon } from "lucide-react"
import type { CSSProperties } from "react"

const toastStyle = {
  "--normal-bg": "var(--popover)",
  "--normal-text": "var(--popover-foreground)",
  "--normal-border": "var(--border)",
  "--border-radius": "var(--radius)",
} as CSSProperties

function Toaster({ ...props }: ToasterProps) {
  return <Sonner
    theme="light"
    className="toaster group"
    closeButton
    icons={{
      success: <CircleCheckIcon className="size-4" />,
      info: <InfoIcon className="size-4" />,
      warning: <TriangleAlertIcon className="size-4" />,
      error: <OctagonXIcon className="size-4" />,
      loading: <Loader2Icon className="size-4 animate-spin" />,
    }}
    style={toastStyle}
    toastOptions={{
      duration: 5000,
      classNames: {
        toast: "max-w-[min(42rem,calc(100vw-2rem))]",
        description: "max-h-40 overflow-y-auto whitespace-normal text-xs leading-5",
      },
    }}
    {...props}
  />
}

export { Toaster }
