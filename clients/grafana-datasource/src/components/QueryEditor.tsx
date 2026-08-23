import React, { ChangeEvent } from "react";
import { InlineField, Input, Select } from "@grafana/ui";
import type { QueryEditorProps, SelectableValue } from "@grafana/data";

import type { DataSource } from "../datasource";
import type { AvuruDataSourceOptions, AvuruQuery, QueryKind } from "../types";

type Props = QueryEditorProps<DataSource, AvuruQuery, AvuruDataSourceOptions>;

const KINDS: Array<SelectableValue<QueryKind>> = [
  { label: "Service RED", value: "services", description: "Rate, errors and latency percentiles per service" },
  { label: "Service health", value: "health", description: "Group status, tier and environment from the health rollup" },
  { label: "Traces", value: "traces", description: "Trace search results as a table" },
  { label: "Cross-zone traffic", value: "zones", description: "Bytes per availability-zone pair" },
];

export function QueryEditor({ query, onChange, onRunQuery }: Props) {
  const kind = query.kind ?? "services";
  const isTraces = kind === "traces";

  const patch = (p: Partial<AvuruQuery>) => {
    onChange({ ...query, ...p });
    onRunQuery();
  };

  return (
    <>
      <InlineField label="Query" labelWidth={16}>
        <Select
          width={28}
          options={KINDS}
          value={kind}
          onChange={(v: SelectableValue<QueryKind>) => patch({ kind: v.value ?? "services" })}
        />
      </InlineField>

      {isTraces && (
        <>
          <InlineField label="Service" labelWidth={16} tooltip="Only traces this service took part in">
            <Input
              width={28}
              value={query.service ?? ""}
              placeholder="any service"
              onChange={(e: ChangeEvent<HTMLInputElement>) => onChange({ ...query, service: e.target.value })}
              onBlur={onRunQuery}
            />
          </InlineField>
          <InlineField label="Status" labelWidth={16}>
            <Select
              width={28}
              options={[
                { label: "Any", value: "" },
                { label: "Error", value: "error" },
                { label: "OK", value: "ok" },
              ]}
              value={query.status ?? ""}
              onChange={(v: SelectableValue<string>) => patch({ status: v.value })}
            />
          </InlineField>
          <InlineField
            label="Tags"
            labelWidth={16}
            tooltip="Attribute filters, e.g. avuru.tag.team=payments — the same filter string the traces screen uses"
          >
            <Input
              width={28}
              value={query.tags ?? ""}
              placeholder="avuru.tag.team=payments"
              onChange={(e: ChangeEvent<HTMLInputElement>) => onChange({ ...query, tags: e.target.value })}
              onBlur={onRunQuery}
            />
          </InlineField>
          <InlineField label="Limit" labelWidth={16}>
            <Input
              width={28}
              type="number"
              value={query.limit ?? 50}
              onChange={(e: ChangeEvent<HTMLInputElement>) =>
                onChange({ ...query, limit: Number(e.target.value) || undefined })
              }
              onBlur={onRunQuery}
            />
          </InlineField>
        </>
      )}

      <InlineField
        label="Project"
        labelWidth={16}
        tooltip="Overrides the data source's default project for this panel"
      >
        <Input
          width={28}
          value={query.project ?? ""}
          placeholder="(data source default)"
          onChange={(e: ChangeEvent<HTMLInputElement>) => onChange({ ...query, project: e.target.value })}
          onBlur={onRunQuery}
        />
      </InlineField>
    </>
  );
}
