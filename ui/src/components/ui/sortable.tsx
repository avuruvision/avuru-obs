"use client";

import { useCallback, useState } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";
import { cn } from "@/lib/cn";

export interface SortColumn<K extends string> {
  key: K;
  label: string;
  // Numeric columns right-align and start their sort largest-first. Text
  // columns start A→Z. That is the "most interesting first" default the
  // inventory tables were all hand-coding as `setSortAsc(key === "name")`.
  numeric?: boolean;
}

export interface ColumnSort<K extends string> {
  sortKey: K;
  sortAsc: boolean;
  toggleSort: (col: SortColumn<K>) => void;
  sortRows: <T extends Partial<Record<K, unknown>>>(rows: T[]) => T[];
}

// Client-side column sorting, shared by the inventory tables (services, green,
// nodes, pods). Every one of them holds a single window's rows and is never
// paginated, so sorting the array in the browser is the whole job — no API
// round-trip, and none of the page-boundary correctness problems that make
// server-side sorting necessary elsewhere.
export function useColumnSort<K extends string>(
  initialKey: K,
  initialAsc = false,
): ColumnSort<K> {
  const [sortKey, setSortKey] = useState<K>(initialKey);
  const [sortAsc, setSortAsc] = useState(initialAsc);

  const toggleSort = useCallback(
    (col: SortColumn<K>) => {
      // Re-clicking the active column flips it; moving to a new one starts in
      // that column's natural direction rather than inheriting the old one,
      // which reads as a random order to the user.
      if (col.key === sortKey) {
        setSortAsc((v) => !v);
        return;
      }
      setSortKey(col.key);
      setSortAsc(!col.numeric);
    },
    [sortKey],
  );

  // Returns a NEW array: callers pass only the rows that participate in the
  // ranking, so a table with pinned rows (green's synthetic roll-ups) can sort
  // the real ones and re-append the rest.
  const sortRows = useCallback(
    <T extends Partial<Record<K, unknown>>>(rows: T[]): T[] => {
      const copy = [...rows];
      copy.sort((a, b) => {
        const av = a[sortKey];
        const bv = b[sortKey];
        const cmp =
          typeof av === "string" && typeof bv === "string"
            ? av.localeCompare(bv)
            : Number(av ?? 0) - Number(bv ?? 0);
        return sortAsc ? cmp : -cmp;
      });
      return copy;
    },
    [sortKey, sortAsc],
  );

  return { sortKey, sortAsc, toggleSort, sortRows };
}

// A sortable header cell. aria-sort is on the <th> (where assistive tech looks
// for it) while the click target is the inner button, so the column is
// reachable by keyboard without making the whole cell focusable.
// iconFirst puts the arrow to the LEFT of the label on numeric columns, so the
// label itself stays flush with the right-aligned figures beneath it. Opt-in
// because the two existing tables differ here and neither should be restyled
// as a side effect of sharing this component.
export function SortableTh<K extends string>({
  col,
  sort,
  className,
  iconFirst = false,
}: {
  col: SortColumn<K>;
  sort: ColumnSort<K>;
  className?: string;
  iconFirst?: boolean;
}) {
  const active = sort.sortKey === col.key;
  return (
    <th
      className={cn(col.numeric && "text-right", className)}
      aria-sort={active ? (sort.sortAsc ? "ascending" : "descending") : undefined}
    >
      <button
        type="button"
        onClick={() => sort.toggleSort(col)}
        className={cn(
          "inline-flex items-center gap-1 hover:text-base-content",
          iconFirst && col.numeric && "flex-row-reverse",
        )}
      >
        {col.label}
        {active &&
          (sort.sortAsc ? (
            <ArrowUp className="h-3 w-3" aria-hidden />
          ) : (
            <ArrowDown className="h-3 w-3" aria-hidden />
          ))}
      </button>
    </th>
  );
}
