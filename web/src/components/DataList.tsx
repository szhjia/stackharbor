import { usePreferences } from "../lib/preferences";
import { useMemo, type ReactNode } from "react";
import { matchesSearch, useListQuery } from "../lib/list-state";
import { Button } from "./ui/button";
import {
  useTable,
  tableFeatures,
  columnFilteringFeature,
  globalFilteringFeature,
  columnVisibilityFeature,
  createFilteredRowModel,
  filterFn_includesString,
  type ColumnDef,
  type RowData,
} from "@tanstack/react-table";
import {
  Table,
  TableHeader,
  TableHead,
  TableBody,
  TableRow,
  TableCell,
} from "./ui/table";
import { Panel } from "./Panel";
import { Input } from "./ui/input";
import { Field } from "./ui/field";
import { Empty, EmptyHeader, EmptyTitle, EmptyDescription } from "./ui/empty";
const features = tableFeatures({
  columnVisibilityFeature,
  columnFilteringFeature,
  globalFilteringFeature,
  filteredRowModel: createFilteredRowModel(),
  filterFns: { includesString: filterFn_includesString },
});
export type ListColumn<T extends RowData> = ColumnDef<typeof features, T, any>;
export function DataList<T extends RowData>({
  data,
  columns,
  label,
  layout = "default",
  empty = "No matching records",
  emptyDescription = "Start a foreground workspace with stackharbor to see its live session.",
  searchText,
  toolbar,
}: {
  data: T[];
  columns: ListColumn<T>[];
  label: string;
  layout?: "default" | "workspaces" | "resources" | "nodes" | "tasks";
  empty?: string;
  emptyDescription?: string;
  searchText?: (item: T) => string;
  toolbar?: ReactNode;
}) {
  const {t: tr} = usePreferences();
  const {search: filter, update} = useListQuery();
  const cols = useMemo(() => columns, [columns]);
  const filtered = useMemo(() => searchText && filter
    ? data.filter(item => matchesSearch(searchText(item), filter)) : data,
  [data, filter, searchText]);
  const table = useTable({
    features,
    data: filtered,
    columns: cols,
    getRowId: (item: T, index: number) => item && typeof item === "object" && "id" in item ? String(item.id) : item && typeof item === "object" && "identity" in item ? String((item.identity as {session_id: string}).session_id) : String(index),
    globalFilterFn: "includesString",
    state: { globalFilter: searchText ? "" : filter },
  });
  return (
    <Panel radius="compact" className="data-list" data-layout={layout}>
      {data.length > 0 || filter || toolbar ? <div className="list-toolbar">
        <Field className="filter-field">
          <Input
            id={`filter-${label}`}
            aria-label={tr("Filter {label}", {label: tr(label)})}
            value={filter}
            onChange={(e) => update("q", e.target.value)}
            placeholder={tr("Find {label}…", {label: tr(label)})}
          />
        </Field>
        {filter ? <Button variant="ghost" onClick={() => update("q", "")}>{tr("Clear search")}</Button> : null}
        <span className="caption list-count" role="status">{tr("{count} of {total} records", {count: table.getRowModel().rows.length, total: data.length})}</span>
        {toolbar}
      </div> : null}
      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((g) => (
            <TableRow key={g.id}>
              {g.headers.map((h) => (
                <TableHead key={h.id} data-column={h.column.id}>
                  <table.FlexRender header={h} />
                </TableHead>
              ))}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {table.getRowModel().rows.map((row) => (
            <TableRow key={row.id}>
              {row.getVisibleCells().map((c) => (
                <TableCell
                  key={c.id}
                  data-column={c.column.id}
                  data-label={
                    typeof c.column.columnDef.header === "string"
                      ? c.column.columnDef.header
                      : undefined
                  }
                >
                  <table.FlexRender cell={c} />
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {table.getRowModel().rows.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{filter ? tr("No matching records") : tr(empty)}</EmptyTitle>
            <EmptyDescription>
              {filter
                ? tr("Try another filter.")
                : tr(emptyDescription)}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : null}
    </Panel>
  );
}
