import type { ReactNode } from "react"
import { AlertTriangle } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"

export function AlertDialog({ children }: { children: ReactNode }) { return <Dialog>{children}</Dialog> }
export const AlertDialogTrigger = DialogTrigger
export function AlertDialogContent({ title, description, onConfirm, confirmLabel = "Confirm" }: { title: string; description: string; onConfirm: () => void; confirmLabel?: string }) {
  return <DialogContent className="rounded-none"><DialogHeader><DialogTitle className="flex items-center gap-2"><AlertTriangle className="size-4 text-amber-700" aria-hidden="true" />{title}</DialogTitle><DialogDescription>{description}</DialogDescription></DialogHeader><DialogFooter><DialogClose render={<Button variant="outline" />}>Cancel</DialogClose><DialogClose render={<Button variant="destructive" onClick={onConfirm} />}>{confirmLabel}</DialogClose></DialogFooter></DialogContent>
}
