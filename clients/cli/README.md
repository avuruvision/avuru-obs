# `avuruobs` — the command-line client

The Hub API is the client-agnostic contract; the web app is one client of it.
This is another, for the places a browser cannot go: a CI job, a deploy script,
a terminal at 3am.

```bash
go install github.com/avuru/avuru-obs/clients/cli/cmd/avuruobs@latest

avuruobs login --url https://obs.example.com --token avurut_…
avuruobs services
avuruobs health --fail-on 'status!=healthy'
```

## Authentication

A [personal API token](../../design/2026-08-13-api-tokens.md), sent as
`Authorization: Bearer avurut_…`. A token resolves to its owner's *live*
permissions, so the CLI sees exactly what that person sees — no parallel
authorization, nothing to keep in sync.

`login` writes the hub URL and token to `~/.avuruobs/config.json` with `0600`
permissions. `AVURUOBS_URL` and `AVURUOBS_TOKEN` override the file, which is how
CI should supply them: a secret in the environment beats a secret on disk.

## Failing a pipeline on a predicate

`--fail-on` takes one comparison and exits **2** when it holds for any row:

```bash
avuruobs services --fail-on 'errorRate>0.05'     # any service over 5% errors
avuruobs services --fail-on 'p95Ms>800'
avuruobs health   --fail-on 'status!=healthy'
```

Exit codes are the contract: `0` nothing matched, `1` the command failed
(network, auth, bad predicate), `2` the predicate matched. A pipeline can tell
"the gate tripped" from "the gate could not run", which a single non-zero exit
cannot.

## Output

`-o table` (default) is for humans, `-o json` is the raw API response — so
anything the CLI does not model yet is still reachable with `jq`.
