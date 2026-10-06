"use client"

// MH-1182: every list of rows in the app is a Nasaq DataTable: the framed table, sortable headers,
// keyboard row focus, row actions on ⋯ and context-click, and its own loading, empty and error states.
// RowsTable is the one-call form for the read-only lists that don't need search or filters; the big
// browsers (contacts, companies, leads) use Nasaq's EntityList, which wraps the same table.
import type { ReactNode } from "react"
import {
  DataTable,
  useDataTable,
  type DataTableColumn,
  type DataTableProps,
  type DataTableRowAction,
  type DataTableSort,
  type TableDensity,
} from "@fadymondy/nasaq/web"

export type { DataTableColumn as RowsColumn, DataTableRowAction as RowsAction }

export function RowsTable<T>({
  data,
  columns,
  getRowId,
  label,
  rowLabel,
  onRowClick,
  rowActions,
  loading,
  error,
  onRetry,
  empty,
  pageSize,
  defaultSort,
  density = "compact",
  frame = true,
  className,
  ...rest
}: {
  data: T[]
  columns: DataTableColumn<T>[]
  getRowId: (row: T) => string
  /** Accessible name of the table. Localise it. */
  label: string
  rowLabel?: (row: T) => string
  onRowClick?: (row: T) => void
  rowActions?: (row: T) => DataTableRowAction[]
  loading?: boolean
  error?: ReactNode
  onRetry?: () => void
  empty?: ReactNode
  pageSize?: number
  defaultSort?: DataTableSort | null
  density?: TableDensity
  frame?: boolean
  className?: string
} & Pick<DataTableProps<T>, "striped" | "bordered" | "hover" | "renderExpanded" | "canExpand"> &
  Record<`data-${string}`, string | number | boolean | undefined>) {
  const table = useDataTable<T>({ data, columns, getRowId, pageSize, defaultSort })
  return (
    <DataTable<T>
      table={table}
      label={label}
      rowLabel={rowLabel}
      onRowClick={onRowClick}
      rowActions={rowActions}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty={empty}
      density={density}
      frame={frame}
      className={className}
      {...rest}
    />
  )
}
