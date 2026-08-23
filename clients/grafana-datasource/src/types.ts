import type { DataSourceJsonData } from "@grafana/data";
import type { DataQuery } from "@grafana/schema";

// What a panel can ask for. A small closed set on purpose: this data source
// brings the numbers Avuru Obs already computes into a dashboard — it is not a
// second query language over them.
export type QueryKind = "services" | "health" | "traces" | "zones";

export interface AvuruQuery extends DataQuery {
  kind: QueryKind;
  /** Only traces a given service took part in. */
  service?: string;
  /** "ok" | "error" — traces only. */
  status?: string;
  /** Attribute filters, e.g. `avuru.tag.team=payments`. */
  tags?: string;
  limit?: number;
  /** Overrides the data source's default project for this panel. */
  project?: string;
}

export const DEFAULT_QUERY: Partial<AvuruQuery> = { kind: "services" };

export interface AvuruDataSourceOptions extends DataSourceJsonData {
  url?: string;
  project?: string;
  timeoutSeconds?: number;
}

// The token is write-only from the browser's side: Grafana encrypts it, the
// backend decrypts it, and the UI only ever learns whether one is configured.
export interface AvuruSecureJsonData {
  apiToken?: string;
}
