# Observability

wdx writes two things you can look at in Grafana:

- **Logs**: every `wdx` run and every daemon request, as JSON lines in `~/.wdx/logs/wdx-<date>.log`. One `debug_id` follows a command through the CLI and the daemon.
- **Metrics**: the daemon measures every request it serves and sends the measurements over OTLP to `otel.endpoint`, by default `http://localhost:4318`.

`wdx observe` runs both in Grafana on your machine, with the all-in-one [`grafana/otel-lgtm`](https://github.com/grafana/docker-otel-lgtm) image under Docker or Podman.

## Start it

```
wdx observe start      # the first start pulls the image, which can take minutes
```

Open http://localhost:3000; it opens on the **wdx** dashboard. Nothing else to set: the daemon already sends to `localhost:4318`.

```
wdx observe status     # running or not, where to open it, which logs it reads
wdx observe stop       # removes the container; the stored logs and metrics stay
wdx observe stop --purge   # also removes them
```

It needs Docker or Podman, used in that order when both are installed, and your user must be able to run it: `docker ps` must work without `sudo`.

## How it fits together

```
daemon ──metrics──► 127.0.0.1:4318 ──► collector ──► Prometheus ─┐
                                                                  ├──► Grafana ◄── you, 127.0.0.1:3000
~/.wdx/logs/*.log ──(read-only mount)──► collector ──► Loki ─────┘
```

- **One container**, `wdx-observability`, runs Grafana, Prometheus (metrics), Loki (logs), Tempo (traces, unused until wdx sends traces), Pyroscope (profiles, unused) and an OpenTelemetry collector. Only ports 3000 and 4318 are published, and only on `127.0.0.1`: Grafana in this image lets anyone in as admin.
- **Logs are read from the files**, not sent: the collector tails `<home>/logs`, mounted read-only, and remembers how far it read. Runs from before the stack started, or from while it was stopped, show up too, back to the log retention (`log.retention_days`). Lines from the CLI are there as well as the daemon's.
- **Metrics are sent** by the daemon every `otel.interval_seconds`. When the stack is not running, the daemon logs `metrics could not be sent` once; requests are never affected, and the counts it sends next include what was missed.
- **Data** lives in the volume `wdx-observability`, kept across starts until `stop --purge`.
- `start` writes the files the container mounts (the collector config, the dashboard) to `<home>/observe`; they come with the `wdx` binary, so a new version brings its own on the next start.

## The dashboard

| Panel | Shows |
|---|---|
| Requests by operation | requests per second, by API operation (`createProject`, `listProjects`, ...) |
| Failures by code | failed requests per second, by error code |
| p95 duration by operation | how long the slowest 5% of requests took |
| Requests in range | a table of every operation, status and code over the time range |
| Commands by outcome | `wdx` runs from their `command returned` lines, including the failures that never reach the daemon (`usage`, `daemon_unavailable`) |
| Errors | every line logged at error level |
| Logs | every line; type a `debug_id` in the box at the top to follow one run |

## The metric

The daemon records one histogram, `wdx.request.duration` in seconds, which Prometheus stores as `wdx_request_duration_seconds_bucket`, `_sum` and `_count`. Its labels:

| Label | Value |
|---|---|
| `operation` | the API operation, as named in `api/openapi.yaml`; absent for a path the API does not have |
| `status` | the HTTP status |
| `code` | the error code, only when the request failed |

`_count` counts requests, so `sum by (code) (increase(wdx_request_duration_seconds_count{code!=""}[1h]))` is the failures of the last hour by code.

## The logs in Loki

Lines are stored under `service_name="wdx"`. The body is the whole JSON line; these fields can be filtered on directly:

`msg`, `debug_id`, `process` (`cli` or `daemon`), `pid`, `command`, `operation`, `outcome`, `exit`, `status`, `code`, `duration_ms`, `project_id`

```
{service_name="wdx"} | debug_id="dbg-0123456789ab"
{service_name="wdx"} | process="daemon" | code="clone_failed"
{service_name="wdx"} | json | response_project_name="app"
```

## Settings

| Key | Default | |
|---|---|---|
| `otel.endpoint` | `http://localhost:4318` | where the daemon sends its metrics; empty sends nothing |
| `otel.interval_seconds` | `15` | how often it sends them |

The daemon reads them when it starts: run `wdx daemon restart` after changing them.

wdx takes its settings only from `config.yaml`, so the OpenTelemetry SDK's `OTEL_*` variables are overridden, with two exceptions the SDK always reads: `OTEL_RESOURCE_ATTRIBUTES`, which can add attributes to the metrics but not change `service.name`, and the experimental `OTEL_GO_X_*` switches.

## Errors

| Code | Exit | When |
|---|---|---|
| `runtime_missing` | 3 | neither Docker nor Podman is installed |
| `port_in_use` | 1 | 3000 or 4318 is taken on 127.0.0.1 |
| `runtime_failed` | 3 | Docker or Podman failed, such as `permission denied` when your user cannot use Docker (add it to the `docker` group and log in again) or the stack stopping while it started; the detail carries the runtime's own message |

## Gotchas

- **One stack per machine.** It reads the logs of the home it was started from; `wdx observe status` shows which.
- **Only the daemon sends metrics.** A command that fails before reaching the daemon is in the logs and the Commands by outcome panel, but not in the metric.
- **SELinux** (Fedora and others): under Podman the mounts are labelled for the container. Under Docker with SELinux enforced, the logs may not be readable; use Podman there.
