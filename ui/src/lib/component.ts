// Per-span component identification (SkyWalking-style): maps a span's
// semconv attributes, instrumentation scope, and kind to a named technology
// with an icon — so a waterfall row reads "PostgreSQL SELECT orders" instead
// of five identical service rows. Detection order: explicit semconv
// attributes (db/messaging/rpc/faas) win over the scope name, which wins
// over generic HTTP attributes, which win over the bare span kind.

import {
  ArrowRightFromLine,
  ArrowRightToLine,
  Braces,
  CircleDot,
  Cpu,
  Database,
  Globe,
  HardDrive,
  Inbox,
  Layers,
  MessageSquare,
  Network,
  Send,
  Zap,
  type LucideIcon,
} from "lucide-react";
import type { Span } from "@/lib/api-types";

export interface SpanComponent {
  name: string; // "PostgreSQL", "Next.js", "Kafka", ...
  category: "db" | "cache" | "messaging" | "rpc" | "http" | "faas" | "runtime" | "internal";
  Icon: LucideIcon;
}

type ComponentInput = Pick<Span, "kind" | "attributes" | "scopeName">;

const CATEGORY_ICON: Record<SpanComponent["category"], LucideIcon> = {
  db: Database,
  cache: HardDrive,
  messaging: MessageSquare,
  rpc: Network,
  http: Globe,
  faas: Zap,
  runtime: Layers,
  internal: CircleDot,
};

// db.system / db.system.name values → display names. Cache systems get the
// cache category (HardDrive icon); anything unlisted is shown capitalized.
const DB_NAMES: Record<string, string> = {
  postgresql: "PostgreSQL",
  mysql: "MySQL",
  mariadb: "MariaDB",
  mssql: "SQL Server",
  sqlserver: "SQL Server",
  "microsoft.sql_server": "SQL Server",
  oracle: "Oracle",
  "oracle.db": "Oracle",
  sqlite: "SQLite",
  mongodb: "MongoDB",
  cassandra: "Cassandra",
  elasticsearch: "Elasticsearch",
  opensearch: "OpenSearch",
  clickhouse: "ClickHouse",
  dynamodb: "DynamoDB",
  "aws.dynamodb": "DynamoDB",
  redis: "Redis",
  valkey: "Valkey",
  memcached: "Memcached",
};
const CACHE_SYSTEMS = new Set(["redis", "valkey", "memcached"]);

const MESSAGING_NAMES: Record<string, string> = {
  kafka: "Kafka",
  rabbitmq: "RabbitMQ",
  nats: "NATS",
  activemq: "ActiveMQ",
  pulsar: "Pulsar",
  rocketmq: "RocketMQ",
  aws_sqs: "SQS",
  "aws.sqs": "SQS",
  gcp_pubsub: "Pub/Sub",
};

const RPC_NAMES: Record<string, string> = {
  grpc: "gRPC",
  apache_dubbo: "Dubbo",
  connect_rpc: "Connect RPC",
  jsonrpc: "JSON-RPC",
};

// Instrumentation-scope substrings → component. Ordered: most specific
// first ("-http" would otherwise shadow "-graphql" etc.). Substring match on
// the lowercased scope covers node ("@opentelemetry/instrumentation-pg"),
// java ("io.opentelemetry.jdbc-3.0"), go ("go.opentelemetry.io/obi"), and
// python ("opentelemetry.instrumentation.flask") naming schemes at once.
const SCOPE_PATTERNS: ReadonlyArray<readonly [string, string, SpanComponent["category"]]> = [
  ["next.js", "Next.js", "runtime"],
  ["instrumentation-express", "Express", "http"],
  ["instrumentation-fastify", "Fastify", "http"],
  ["instrumentation-nestjs", "NestJS", "runtime"],
  ["instrumentation-graphql", "GraphQL", "rpc"],
  ["instrumentation-undici", "HTTP Client", "http"],
  ["instrumentation-fetch", "HTTP Client", "http"],
  ["instrumentation-grpc", "gRPC", "rpc"],
  ["instrumentation-pg", "PostgreSQL", "db"],
  ["instrumentation-mysql", "MySQL", "db"],
  ["instrumentation-ioredis", "Redis", "cache"],
  ["instrumentation-redis", "Redis", "cache"],
  ["instrumentation-mongodb", "MongoDB", "db"],
  ["instrumentation-mongoose", "MongoDB", "db"],
  ["instrumentation-kafkajs", "Kafka", "messaging"],
  ["instrumentation-amqplib", "RabbitMQ", "messaging"],
  ["instrumentation-aws-sdk", "AWS SDK", "rpc"],
  ["instrumentation-http", "HTTP", "http"],
  ["io.opentelemetry.jdbc", "JDBC", "db"],
  ["io.opentelemetry.hikaricp", "HikariCP", "db"],
  ["io.opentelemetry.hibernate", "Hibernate", "db"],
  ["io.opentelemetry.spring-webflux", "Spring WebFlux", "http"],
  ["io.opentelemetry.spring-web", "Spring Web", "http"],
  ["io.opentelemetry.tomcat", "Tomcat", "http"],
  ["io.opentelemetry.netty", "Netty", "http"],
  ["io.opentelemetry.lettuce", "Redis (Lettuce)", "cache"],
  ["io.opentelemetry.kafka-clients", "Kafka", "messaging"],
  ["io.opentelemetry.okhttp", "HTTP Client", "http"],
  ["io.opentelemetry.apache-httpclient", "HTTP Client", "http"],
  ["go.opentelemetry.io/obi", "eBPF", "runtime"],
  ["otelhttp", "HTTP", "http"],
  ["otelgrpc", "gRPC", "rpc"],
  ["net/http", "HTTP", "http"],
  ["gorm.io", "GORM", "db"],
  ["gin-gonic", "Gin", "http"],
  ["instrumentation.flask", "Flask", "http"],
  ["instrumentation.django", "Django", "http"],
  ["instrumentation.fastapi", "FastAPI", "http"],
  ["instrumentation.requests", "HTTP Client", "http"],
  ["instrumentation.urllib3", "HTTP Client", "http"],
  ["instrumentation.httpx", "HTTP Client", "http"],
  ["instrumentation.psycopg", "PostgreSQL", "db"],
  ["instrumentation.sqlalchemy", "SQLAlchemy", "db"],
  ["instrumentation.celery", "Celery", "messaging"],
  ["instrumentation.boto", "AWS SDK", "rpc"],
  ["botocore", "AWS SDK", "rpc"],
];

const KIND_FALLBACK: Record<string, SpanComponent> = {
  Server: { name: "Server", category: "http", Icon: ArrowRightToLine },
  Client: { name: "Client", category: "http", Icon: ArrowRightFromLine },
  Producer: { name: "Producer", category: "messaging", Icon: Send },
  Consumer: { name: "Consumer", category: "messaging", Icon: Inbox },
};

const capitalize = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) : s);

function make(name: string, category: SpanComponent["category"], Icon?: LucideIcon): SpanComponent {
  return { name, category, Icon: Icon ?? CATEGORY_ICON[category] };
}

export function spanComponent(span: ComponentInput): SpanComponent {
  const attrs = span.attributes ?? {};

  const db = (attrs["db.system.name"] ?? attrs["db.system"] ?? "").toLowerCase();
  if (db) {
    const category = CACHE_SYSTEMS.has(db) ? "cache" : "db";
    return make(DB_NAMES[db] ?? capitalize(db), category);
  }

  const msg = (attrs["messaging.system"] ?? "").toLowerCase();
  if (msg) {
    const icon =
      span.kind === "Producer" ? Send : span.kind === "Consumer" ? Inbox : MessageSquare;
    return make(MESSAGING_NAMES[msg] ?? capitalize(msg), "messaging", icon);
  }

  const rpc = (attrs["rpc.system"] ?? "").toLowerCase();
  if (rpc) return make(RPC_NAMES[rpc] ?? capitalize(rpc), "rpc");

  if (attrs["faas.trigger"] || attrs["faas.name"]) return make("FaaS", "faas");

  if (attrs["next.span_type"]) return make("Next.js", "runtime");

  const scope = (span.scopeName ?? "").toLowerCase();
  if (scope) {
    for (const [match, name, category] of SCOPE_PATTERNS) {
      if (scope.includes(match)) {
        const icon = name === "GraphQL" ? Braces : name === "eBPF" ? Cpu : undefined;
        return make(name, category, icon);
      }
    }
  }

  if (attrs["http.request.method"] || attrs["http.method"] || attrs["url.full"] || attrs["http.url"]) {
    return make("HTTP", "http");
  }

  if (scope) {
    // Unknown library: show its last path segment rather than nothing.
    const tail = scope.split("/").pop()?.split(".").pop() ?? scope;
    return make(capitalize(tail), "internal");
  }

  return KIND_FALLBACK[span.kind] ?? make("Internal", "internal");
}

// kindIcon is the SkyWalking-style entry/exit glyph: requests entering the
// service (Server/Consumer) vs leaving it (Client/Producer). Internal spans
// get no glyph.
export function kindIcon(kind: string): LucideIcon | null {
  switch (kind) {
    case "Server":
    case "Consumer":
      return ArrowRightToLine;
    case "Client":
    case "Producer":
      return ArrowRightFromLine;
    default:
      return null;
  }
}

// spanPeer extracts the remote endpoint of an outgoing span ("songs:80",
// "db-host:5432") — shown dimmed after the operation, SkyWalking-style. Only
// meaningful for the calling side.
export function spanPeer(span: Pick<Span, "kind" | "attributes">): string | null {
  if (span.kind !== "Client" && span.kind !== "Producer") return null;
  const a = span.attributes ?? {};
  const hostPort = (host?: string, port?: string) =>
    host ? (port ? `${host}:${port}` : host) : null;
  return (
    hostPort(a["server.address"], a["server.port"]) ??
    hostPort(a["net.peer.name"], a["net.peer.port"]) ??
    a["peer.service"] ??
    urlHost(a["url.full"] ?? a["http.url"]) ??
    hostPort(a["net.peer.ip"], a["net.peer.port"]) ??
    hostPort(a["network.peer.address"], a["network.peer.port"]) ??
    null
  );
}

function urlHost(u: string | undefined): string | null {
  if (!u) return null;
  try {
    const url = new URL(u);
    return url.port ? `${url.hostname}:${url.port}` : url.hostname;
  } catch {
    return null;
  }
}
