// Grafana loads a plugin's module.js through SystemJS, so the bundle is AMD
// with every Grafana package and React left external — a plugin that bundled
// its own React would fight the one the host already runs.
//
// Hand-written rather than pulled from a scaffolding package: the whole config
// is thirty lines, and a build we can read is worth more here than one we
// inherit.
const path = require("path");
const CopyWebpackPlugin = require("copy-webpack-plugin");
const ReplaceInFileWebpackPlugin = require("replace-in-file-webpack-plugin");

const pluginJson = require("./src/plugin.json");

module.exports = (env) => ({
  mode: env.production ? "production" : "development",
  devtool: env.production ? false : "source-map",
  target: "web",
  context: path.join(process.cwd(), "src"),
  entry: { module: "./module.ts" },
  output: {
    clean: true,
    filename: "[name].js",
    library: { type: "amd" },
    path: path.resolve(process.cwd(), "dist"),
    publicPath: `public/plugins/${pluginJson.id}/`,
    uniqueName: pluginJson.id,
  },
  resolve: { extensions: [".ts", ".tsx", ".js", ".jsx"] },
  module: {
    rules: [
      {
        test: /\.tsx?$/,
        exclude: /node_modules/,
        use: { loader: "ts-loader", options: { transpileOnly: true } },
      },
    ],
  },
  // Everything Grafana already ships. Bundling any of it would either bloat the
  // plugin or break the host's singletons (React hooks in particular).
  externals: [
    "react",
    "react-dom",
    "@grafana/data",
    "@grafana/runtime",
    "@grafana/schema",
    "@grafana/ui",
    ({ request }, callback) =>
      request?.startsWith("@grafana/") ? callback(undefined, `amd ${request}`) : callback(),
  ],
  plugins: [
    new CopyWebpackPlugin({
      patterns: [
        { from: "plugin.json", to: "." },
        { from: "img/**/*", to: "." },
        { from: "../README.md", to: "." },
        { from: "../CHANGELOG.md", to: ".", noErrorOnMissing: true },
        { from: "../LICENSE", to: ".", noErrorOnMissing: true },
      ],
    }),
    // The version and date in plugin.json are placeholders in the repo so the
    // source never carries a stale release number; the build stamps them.
    new ReplaceInFileWebpackPlugin([
      {
        dir: "dist",
        files: ["plugin.json"],
        rules: [
          { search: /\%VERSION\%/g, replace: process.env.PLUGIN_VERSION || "0.0.0-dev" },
          { search: /\%TODAY\%/g, replace: new Date().toISOString().slice(0, 10) },
        ],
      },
    ]),
  ],
});
