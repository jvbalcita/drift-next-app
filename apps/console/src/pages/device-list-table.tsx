import type { ReactNode } from "react"
import { ChevronLeft, ChevronRight } from "lucide-react"
import { Button } from "@/components/ui/button"
import type { ControlPlaneSnapshot, DeviceView } from "@/lib/domain/control-plane"
import { DeviceStatus } from "./shared"

/**
 * The Devices dialog's table, and the ONE statement of what its columns are.
 *
 * The table used to be written out inline, header cell by header cell, inside a
 * `sm` dialog: five columns read cut off, and the only way to change which of
 * them an operator saw was to edit this file. The column set is stated once
 * here, as data, and the table is drawn from it - so the header a reader sees,
 * the cell that fills it and the control that moves or hides it cannot disagree
 * about which columns exist, and adding a column is a row in this table rather
 * than a third place that has to know about the first two.
 */

/** The columns the Devices dialog draws, named by the thing each one reports. */
export type DeviceListColumnId = "index" | "name" | "id" | "status" | "port"

/** What a column's cell is given: the device, and the endpoints its port is read from. */
export interface DeviceListColumnContext {
  endpoints: ControlPlaneSnapshot["endpoints"]
}

export interface DeviceListColumn {
  id: DeviceListColumnId
  /** The column's own name: the header cell AND the label of its controls. */
  label: string
  headerClassName: string
  cellClassName: string
  /** The column's own width rule, so a column stays readable wherever it is moved to. */
  widthClassName: string
  cell: (device: DeviceView, context: DeviceListColumnContext, rowIndex: number) => ReactNode
}

/**
 * The observed port is the one the device was SEEN at, or nothing.
 *
 * Painting one field into every row would show a port no device answered on, so a
 * device with no current endpoint reads as an em dash rather than as a port.
 */
function observedPort(device: DeviceView, context: DeviceListColumnContext): string {
  const endpoint = context.endpoints.find((candidate) => candidate.deviceId === device.id && candidate.state === "current")
  return endpoint ? String(endpoint.port) : "—"
}

export const deviceListColumns: readonly DeviceListColumn[] = [
  { id: "index", label: "Index", headerClassName: "font-semibold", cellClassName: "font-mono text-muted-foreground", widthClassName: "w-16", cell: (_device, _context, rowIndex) => rowIndex + 1 },
  { id: "name", label: "Device Name", headerClassName: "font-semibold", cellClassName: "font-medium", widthClassName: "min-w-44", cell: (device) => device.displayName },
  { id: "id", label: "Device ID", headerClassName: "font-semibold", cellClassName: "font-mono text-[10px] text-muted-foreground", widthClassName: "min-w-44", cell: (device) => device.id },
  { id: "status", label: "Status", headerClassName: "font-semibold", cellClassName: "", widthClassName: "min-w-32", cell: (device) => <DeviceStatus device={device} /> },
  { id: "port", label: "Observed Port", headerClassName: "font-semibold", cellClassName: "font-mono", widthClassName: "min-w-28", cell: (device, context) => observedPort(device, context) },
]

/** The columns in their own order, which is the order a caller starts from. */
export const deviceListColumnIds: readonly DeviceListColumnId[] = deviceListColumns.map((column) => column.id)

export function deviceListColumn(id: DeviceListColumnId): DeviceListColumn {
  const column = deviceListColumns.find((candidate) => candidate.id === id)
  // The set above is closed and the ids are its own, so this cannot be reached
  // with an id no column carries; the throw is the compiler's business, not a
  // runtime path an operator can take.
  if (!column) throw new Error(`unknown device list column: ${id}`)
  return column
}

/**
 * DeviceListTable draws the registry with the operator's own column order.
 *
 * Its columns are reorderable and filterable IN PLACE: each header carries the
 * two controls that move its column one place, and the caller decides which
 * columns are hidden. A hidden column is never simply absent - the surface that
 * hides it states which columns are not being shown (see the dialog), because a
 * table missing its Status column reads as a table that has no status rather
 * than as one that is withholding it.
 */
export function DeviceListTable({ devices, endpoints, columns, onMoveColumn, describedBy }: {
  devices: readonly DeviceView[]
  endpoints: ControlPlaneSnapshot["endpoints"]
  columns: readonly DeviceListColumnId[]
  onMoveColumn: (id: DeviceListColumnId, direction: -1 | 1) => void
  describedBy?: string
}) {
  const context: DeviceListColumnContext = { endpoints }
  return <div className="max-h-[60vh] overflow-auto border border-border">
    <table className="w-full min-w-[880px] text-left text-xs" aria-describedby={describedBy}>
      <caption className="sr-only">Devices in this workspace&apos;s registry, with the columns this operator has chosen to read</caption>
      <thead className="sticky top-0 bg-muted/80 text-[10px] uppercase tracking-[.08em] text-muted-foreground">
        <tr>
          {columns.map((id, index) => {
            const column = deviceListColumn(id)
            return <th key={column.id} scope="col" className={`px-3 py-2 ${column.headerClassName} ${column.widthClassName}`}>
              <span className="flex items-center gap-1">
                <span className="min-w-0 flex-1">{column.label}</span>
                <Button type="button" size="icon-sm" variant="ghost" className="size-5 shrink-0" aria-label={`Move ${column.label} column earlier`} disabled={index === 0} onClick={() => onMoveColumn(column.id, -1)}><ChevronLeft className="size-3" aria-hidden="true" /></Button>
                <Button type="button" size="icon-sm" variant="ghost" className="size-5 shrink-0" aria-label={`Move ${column.label} column later`} disabled={index === columns.length - 1} onClick={() => onMoveColumn(column.id, 1)}><ChevronRight className="size-3" aria-hidden="true" /></Button>
              </span>
            </th>
          })}
        </tr>
      </thead>
      <tbody className="divide-y divide-border">
        {devices.map((device, rowIndex) => <tr key={device.id}>
          {columns.map((id) => {
            const column = deviceListColumn(id)
            return <td key={column.id} className={`px-3 py-3 ${column.cellClassName} ${column.widthClassName}`}>{column.cell(device, context, rowIndex)}</td>
          })}
        </tr>)}
      </tbody>
    </table>
  </div>
}
