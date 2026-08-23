import React, { ChangeEvent } from "react";
import { InlineField, Input, SecretInput } from "@grafana/ui";
import type { DataSourcePluginOptionsEditorProps } from "@grafana/data";

import type { AvuruDataSourceOptions, AvuruSecureJsonData } from "../types";

type Props = DataSourcePluginOptionsEditorProps<AvuruDataSourceOptions, AvuruSecureJsonData>;

export function ConfigEditor({ options, onOptionsChange }: Props) {
  const { jsonData, secureJsonFields, secureJsonData } = options;

  const setJson = (patch: Partial<AvuruDataSourceOptions>) =>
    onOptionsChange({ ...options, jsonData: { ...jsonData, ...patch } });

  const onTokenChange = (event: ChangeEvent<HTMLInputElement>) =>
    onOptionsChange({
      ...options,
      secureJsonData: { apiToken: event.target.value },
    });

  // Resetting clears the stored secret as well as the field, so "reset" means
  // what it says rather than leaving the old token in place.
  const onTokenReset = () =>
    onOptionsChange({
      ...options,
      secureJsonFields: { ...secureJsonFields, apiToken: false },
      secureJsonData: { ...secureJsonData, apiToken: "" },
    });

  return (
    <>
      <InlineField label="Hub URL" labelWidth={20} interactive tooltip="Base URL of the Avuru Obs hub, e.g. https://obs.example.com">
        <Input
          id="avuru-url"
          width={40}
          value={jsonData.url ?? ""}
          placeholder="https://obs.example.com"
          onChange={(e: ChangeEvent<HTMLInputElement>) => setJson({ url: e.target.value })}
        />
      </InlineField>

      <InlineField
        label="API token"
        labelWidth={20}
        interactive
        tooltip="A personal API token from Settings → Access. It is stored encrypted and only ever decrypted by this plugin's backend — it never reaches a browser. The data source sees exactly what the token's owner sees."
      >
        <SecretInput
          required
          id="avuru-token"
          width={40}
          isConfigured={Boolean(secureJsonFields?.apiToken)}
          value={secureJsonData?.apiToken ?? ""}
          placeholder="avurut_…"
          onReset={onTokenReset}
          onChange={onTokenChange}
        />
      </InlineField>

      <InlineField
        label="Default project"
        labelWidth={20}
        interactive
        tooltip="Project (tenant) to read when a panel does not name one. Leave empty to use the token owner's default."
      >
        <Input
          id="avuru-project"
          width={40}
          value={jsonData.project ?? ""}
          placeholder="default"
          onChange={(e: ChangeEvent<HTMLInputElement>) => setJson({ project: e.target.value })}
        />
      </InlineField>

      <InlineField
        label="Timeout (s)"
        labelWidth={20}
        interactive
        tooltip="How long a hub call may take. A dashboard that hangs is worse than one panel that says it timed out."
      >
        <Input
          id="avuru-timeout"
          type="number"
          width={40}
          value={jsonData.timeoutSeconds ?? 30}
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            setJson({ timeoutSeconds: Number(e.target.value) || undefined })
          }
        />
      </InlineField>
    </>
  );
}
