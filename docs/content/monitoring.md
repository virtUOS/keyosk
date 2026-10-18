# Monitoring

## Metrics

Prometheus metrics are served on `/metrics`, labelled by route template so a
per-user path does not create a time series per user. Scrapers that send an
OpenMetrics `Accept` header (Prometheus does by default) get the OpenMetrics
format; other clients get the classic Prometheus text format.

Set `METRICS_TOKEN` to require a bearer token, for example one made with
`openssl rand -hex 32`. Requests without it get `401`. Without the setting the
endpoint answers anyone who can reach it, and Keyosk warns about that at
startup. In Prometheus, put the token in a file and point the scrape job at it:

```yaml
scrape_configs:
  - job_name: keyosk
    scheme: https
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/keyosk-metrics-token
```

For a quick look, `curl -H "Authorization: Bearer $METRICS_TOKEN" https://…/metrics`.

| Metric | Meaning |
| ------ | ------- |
| `keyosk_http_requests_total` | requests by route, method, status |
| `keyosk_http_request_duration_seconds` | latency by route |
| `keyosk_key_operations_total` | key issue/extend/delete/revoke by outcome |
| `keyosk_keys_marked_missing_total` | keys marked as missing because the gateway no longer had them, by where Keyosk noticed (`dashboard`, `extend`, `sync`) |
| `keyosk_keys_missing` | keys marked as missing right now, waiting out the grace period; counted from the database on every scrape |
| `keyosk_keys_missing_removed_total` | marked keys removed after the grace period ran out |
| `keyosk_active_keys` | keys currently issued |
| `keyosk_keys_expiring_7d` | keys expiring within a week |
| `keyosk_job_last_success_timestamp_seconds` | when each background job last ran without error, by `task`; the job's start time until it first succeeds |
| `keyosk_job_interval_seconds` | how often each background job runs, by `task` |
| `keyosk_job_item_failures_total` | notices, reversions or limit pushes a background job could not get done and retries on its next run, by `task` |

The missing-key metrics are the alarm for a Keyosk instance pointed at the
wrong gateway, or at one restored from a backup. Suggested alerts: a warning
when `keyosk_keys_missing` stays above zero for an hour, or when
`keyosk_keys_marked_missing_total` rises; and a critical alert when
`keyosk_keys_missing_removed_total` rises, since a record was lost. The first
fires while the grace period (`KEY_MISSING_GRACE_PERIOD`) still lets you
correct the cause without losing anything.

The job metrics show whether the background jobs still run. The `task` label
names the job: `expiry_reminder` (expiry notices, every 6 hours), `profile_expiry` (reverting
temporary profile assignments, every 15 minutes), `limit_sync` (pushing
profile limits to the gateway, every `LIMIT_SYNC_INTERVAL`) and
`session_cleanup` (hourly). A run counts as a success when it gets through,
even if a single notice or key failed along the way; those failures are
logged and retried. One alert covers all of them, firing when a job has not
succeeded for two of its intervals, whether it stopped, keeps failing or is
still working on one run:

```promql
time() - keyosk_job_last_success_timestamp_seconds > 2 * keyosk_job_interval_seconds
```

Before its first success, a job's timestamp is the time it started, so a job
that fails from the start alerts as well, without firing on every restart.

A run that gets through with some of its items failing still counts as a
success, so a mail relay or gateway that keeps refusing does not show up
there. `keyosk_job_item_failures_total` counts those items. A single failure
is retried on the next run and needs no attention; suggested is a warning when
failures keep coming for a day, which clears by itself once the retries get
through:

```yaml
- alert: KeyoskJobItemsFailing
  expr: increase(keyosk_job_item_failures_total[12h]) > 0
  for: 1d
```

The range covers two runs of the slowest job and `for` outlasts it, so one
failed item does not fire; a job that fails something in every half day for a
whole day does. Without the `for`, or with one shorter than the range, a single
failure would fire and stay up until it left the range.

## Health checks

Two endpoints answer without a login, for load balancers and orchestrators:

- `GET /healthz` answers `200 ok` while the server is running. Use it as the
  liveness probe.
- `GET /readyz` also checks the database, and answers `503` when it cannot
  reach it. Use it as the readiness probe.

The container image needs no shell or `curl` for this: its `HEALTHCHECK` runs
`keyosk -healthcheck`, which probes `/healthz` on `LISTEN_ADDR` and exits
non-zero when that fails. Kubernetes ignores the image's health check and uses
its own probes on the two endpoints.
