import { DataSourcePlugin } from "@grafana/data";

import { ConfigEditor } from "./components/ConfigEditor";
import { QueryEditor } from "./components/QueryEditor";
import { DataSource } from "./datasource";
import { AvuruDataSourceOptions, AvuruQuery } from "./types";

export const plugin = new DataSourcePlugin<DataSource, AvuruQuery, AvuruDataSourceOptions>(DataSource)
  .setConfigEditor(ConfigEditor)
  .setQueryEditor(QueryEditor);
