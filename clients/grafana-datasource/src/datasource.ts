import { CoreApp, DataSourceInstanceSettings } from "@grafana/data";
import { DataSourceWithBackend } from "@grafana/runtime";

import { AvuruDataSourceOptions, AvuruQuery, DEFAULT_QUERY } from "./types";

// Every query goes through the backend: the API token never reaches a browser,
// and a hub reachable only inside the cluster still works because the request
// leaves the Grafana server.
export class DataSource extends DataSourceWithBackend<AvuruQuery, AvuruDataSourceOptions> {
  constructor(instanceSettings: DataSourceInstanceSettings<AvuruDataSourceOptions>) {
    super(instanceSettings);
  }

  getDefaultQuery(_app: CoreApp): Partial<AvuruQuery> {
    return DEFAULT_QUERY;
  }

  // A panel with no query kind chosen yet would otherwise fire a request the
  // backend has to reject.
  filterQuery(query: AvuruQuery): boolean {
    return Boolean(query.kind);
  }
}
