# buddy

A small DNS server in Go that auto-publishes A records for
Docker containers based on their `caddy` labels.

It watches the Docker socket, parses any label whose key starts with `caddy`
(including numbered variants like `caddy.0`, `caddy.1`), extracts the hostname,
and serves it as an A record over UDP and TCP on port 53.

## Features

- Authoritative for a single zone (default `local.lan.`)
- Parses `caddy[.N]` labels, strips ports (`myapp.local.lan:80` → `myapp.local.lan`)
- Appends the zone if the label doesn't already end with it
- Watches Docker `start` / `die` / `stop` / `destroy` events and re-syncs in real time
- Reconnects to the event stream with exponential backoff (max 30s)
- Logs every record add / remove
- Concurrency-safe records map (`sync.RWMutex`)
- Returns `NXDOMAIN` for unknown names, `SERVFAIL` on panics
- All responses set the Authoritative flag, TTL 30s

## Configuration

| Variable             | Default                       | Description          |
| -------------------- | ----------------------------- | -------------------- |
| `DNS_PORT`           | `53`                          | Port to listen on    |
| `DNS_ZONE`           | `local.lan.`                  | Zone to serve        |
| `DNS_TTL`            | `30`                          | TTL in seconds       |
| `DOCKER_HOST`        | `unix:///var/run/docker.sock` | Docker socket path   |
| `CADDY_LABEL_PREFIX` | `caddy`                       | Label prefix to scan |
| `LOG_LEVEL`          | `info`                        | `info` or `debug`    |
| `PUBLISH_IP`         | `host`                        | `host` returns the docker host's LAN IP (reachable from other machines); `container` returns the container's bridge IP (only reachable on the host itself) |
| `HOST_IP`            | _(auto)_                      | Override for the host IP when `PUBLISH_IP=host`. If unset, resolved from `DOCKER_HOST` (if `tcp://`) or the default outbound interface |

## Local development

Uses [mise](https://mise.jdx.dev) to manage the Go toolchain and tasks.

```sh
mise install      # install Go 1.26
mise run test     # go test ./...
mise run build    # compile to ./bin/buddy
mise run dev      # run the server locally (auto-detects the Docker socket, listens on :5553)
```

## Validation

```sh
mise run dev
docker run --rm --name testapp --label caddy=testapp.local.lan nginx:alpine
dig @127.0.0.1 -p 5553 testapp.local.lan +short    # → container IP
docker stop testapp
dig @127.0.0.1 -p 5553 testapp.local.lan +short    # → empty (NXDOMAIN)
```

The server logs should show `dns record added` on `docker run` and
`dns record removed` on `docker stop`.

## Docker / docker-compose

```sh
docker compose up -d
```

Point a client at the host running the container (port 53). Containers with a
label like:

```
caddy: myapp.local.lan
caddy.0: api.local.lan:80
```

become DNS-resolvable as `myapp.local.lan` and `api.local.lan`.

## CI / publishing

`.github/workflows/build.yml` runs tests on every push/PR and, on pushes to
`main` or version tags (`v*.*.*`), publishes a multi-arch image
(`linux/amd64`, `linux/arm64`) to `ghcr.io/<owner>/<repo>`.
