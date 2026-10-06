import { useMemo, useState } from "react";
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
import { Input } from "./ui/input";
import { Field, FieldLabel } from "./ui/field";
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
  empty = "No matching records",
}: {
  data: T[];
  columns: ListColumn<T>[];
  label: string;
  empty?: string;
}) {
  const [filter, setFilter] = useState("");
  const cols = useMemo(() => columns, [columns]);
  const table = useTable({
    features,
    data,
    columns: cols,
    globalFilterFn: "includesString",
    state: { globalFilter: filter },
    onGlobalFilterChange: setFilter,
  });
  return (
    <div className="data-list">
      <Field className="filter-field">
        <FieldLabel htmlFor={`filter-${label}`}>Filter {label}</FieldLabel>
        <Input
          id={`filter-${label}`}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={`Find ${label}…`}
        />
      </Field>
      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((g) => (
            <TableRow key={g.id}>
              {g.headers.map((h) => (
                <TableHead key={h.id}>
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
            <EmptyTitle>{empty}</EmptyTitle>
            <EmptyDescription>
              {filter
                ? "Try another filter."
                : "Start a foreground workspace with stackharbor to see its live session."}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : null}
    </div>
  );
}
