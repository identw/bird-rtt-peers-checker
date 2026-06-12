# bird-rtt-keeper

A daemon for monitoring [BIRD 2](https://bird.network.cz/) BGP peers and automatically disabling/enabling protocols when link quality degrades.

The utility connects to the bird control socket (`bird.ctl`), discovers BGP neighbors, periodically checks their reachability via ICMP and (optionally) TCP, and runs `disable` on the protocol in bird when failures accumulate. After the link recovers and the backoff pause expires, it runs `enable`.

It also exports metrics in Prometheus format.

## Operating modes

### `bird-rtt-checker` (default)

Main mode. For each BGP peer from bird:

1. Runs an ICMP check (ping with RTT and packet loss analysis).
2. Optionally — a TCP check (upload/download via the tcpcheck protocol on the remote side).
3. On a streak of failures — disables the BGP protocol in bird (`disable <peer>`).
4. On recovery and after the backoff pause — re-enables it (`enable <peer>`).
5. Every 2 minutes re-reads the BGP peer list and BFD session state.
6. Serves metrics on HTTP `/metrics`.

### `tcpcheck-server`

TCP server that accepts health checks from remote hosts running `bird-rtt-checker`. Accepts connections and runs upload/download tests with data integrity verification. Used by peers during TCP health checks.

## Requirements

- Go 1.24+ (to build from source)
- BIRD 2 with configured BGP peers
- Access to the bird Unix socket: `/run/bird/bird.ctl` (hardcoded)
- For ICMP checks: raw socket privileges (`CAP_NET_RAW`) or run as root
- For TCP checks: `tcpcheck-server` must be running on the peer side (default port `32486`)

## Build

```bash
go build -o bird-rtt-keeper ./cmd/
```

The CI/release binary is named `bird-rtt-peers-checker`.

## Running

### Main mode

```bash
./bird-rtt-keeper
```

Minimal example with TCP checks disabled:

```bash
./bird-rtt-keeper --tcpcheck=false
```

Strict mode — TCP check affects peer disable/enable:

```bash
./bird-rtt-keeper --tcpcheck-enforce
```

### TCP server (on the peer side)

```bash
./bird-rtt-keeper --mode=tcpcheck-server --ports=32486
```

Multiple ports:

```bash
./bird-rtt-keeper --mode=tcpcheck-server --ports=32486,32487
```

## Command-line options

| Flag                 | Default            | Description                                                                                  |
| -------------------- | ------------------ | -------------------------------------------------------------------------------------------- |
| `--mode`             | `bird-rtt-checker` | Mode: `bird-rtt-checker` or `tcpcheck-server`                                                 |
| `--ports`            | `32486`            | Comma-separated list of TCP ports (for `tcpcheck-server` only)                               |
| `--tcpcheck`         | `true`             | Enable TCP health check for BGP peers                                                        |
| `--tcpcheck-enforce` | `false`            | Use TCP check results for peer disable/enable. Requires `--tcpcheck`                         |
| `--metrics`          | `true`             | Enable Prometheus exporter                                                                   |
| `--metrics-listen`   | `127.0.0.1:9574`   | HTTP listen address for metrics                                                              |

## Environment variables

| Variable | Used in           | Description                                                                                  |
| -------- | ----------------- | -------------------------------------------------------------------------------------------- |
| `PORTS`  | `tcpcheck-server` | Comma-separated list of ports. Used only when `--ports` is not set or empty                   |

The main `bird-rtt-checker` mode does **not** use environment variables. The bird socket path (`/run/bird/bird.ctl`) is hardcoded.

## Check logic

### ICMP

Each cycle — 28 packets, 2 s interval, 58 s timeout. The check fails if:

| Criterion      | Threshold |
| -------------- | --------- |
| Packet loss    | > 20%     |
| Average RTT    | > 200 ms  |
| Maximum RTT    | > 800 ms  |
| Minimum RTT    | > 100 ms  |
| RTT StdDev     | > 80 ms   |

### TCP

Interval between checks: 5 minutes + jitter 30–270 s. Each check — 2 download and 2 upload attempts of 10 MB (20 s timeout). The check fails if:

| Criterion                         | Threshold |
| --------------------------------- | --------- |
| Error rate                        | > 20%     |
| Average duration                  | > 12 s    |
| Maximum duration                  | > 18 s    |
| Minimum duration                  | > 12 s    |
| Duration StdDev (≥ 5 attempts)    | > 7 s     |

Connection port: `32486` (hardcoded in `TcpChecker`).

### Peer disable and enable

| Check                          | Consecutive failures to disable | Consecutive successes to reset pause |
| ------------------------------ | ------------------------------- | ------------------------------------ |
| ICMP                           | 3                               | 8                                    |
| TCP (with `--tcpcheck-enforce`)| 2                               | 4                                    |

- On disable: `birdc disable <peer>`, initial pause **150 s**, doubles on each subsequent disable.
- Re-enable is only possible after the current pause expires and checks succeed.
- Pause resets to 0 if ≥ 45 minutes have passed since the last disable and enough successful checks have accumulated.

With `--tcpcheck` but without `--tcpcheck-enforce`, the TCP check still runs and appears in `bird_rtt_keeper_tcp_alive`, but does **not** affect disable/enable or `bird_rtt_keeper_host_alive`.

## Prometheus metrics

Endpoint: `http://<metrics-listen>/metrics` (default `http://127.0.0.1:9574/metrics`).

Common labels for most metrics: `peer` (BGP protocol name in bird), `peer_ip` (neighbor IP). Time series are removed when a peer is deleted.

### Health check (keeper)

| Metric                                     | Labels                      | Type  | Description                                                                                                      |
| ------------------------------------------ | --------------------------- | ----- | ---------------------------------------------------------------------------------------------------------------- |
| `bird_rtt_keeper_host_alive`               | `peer`, `peer_ip`           | gauge | Host passes all **enabled** checks (`1`/`0`). ICMP always; TCP only with `--tcpcheck --tcpcheck-enforce`           |
| `bird_rtt_keeper_icmp_alive`               | `peer`, `peer_ip`           | gauge | Last ICMP check succeeded                                                                                        |
| `bird_rtt_keeper_tcp_alive`                | `peer`, `peer_ip`           | gauge | Last TCP check succeeded. Only when `--tcpcheck`                                                                   |
| `bird_rtt_keeper_peer_enabled`             | `peer`, `peer_ip`           | gauge | Keeper has not disabled the protocol in bird (`1` = enabled)                                                     |
| `bird_rtt_keeper_pause_remaining_seconds`  | `peer`, `peer_ip`           | gauge | Seconds until re-enable is possible after disable                                                                  |
| `bird_rtt_keeper_consecutive_failures`     | `peer`, `peer_ip`, `check`  | gauge | Consecutive failed checks. `check`: `icmp` or `tcp` (tcp = actual result, regardless of enforce)                 |
| `bird_rtt_keeper_consecutive_successes`    | `peer`, `peer_ip`, `check`  | gauge | Consecutive successful checks                                                                                    |
| `bird_rtt_keeper_last_disable_reason_info` | `peer`, `peer_ip`, `reason` | gauge | Reason for the last disable (value is always `1`). `reason=none` if never disabled                               |
| `bird_rtt_keeper_last_check_timestamp`     | `peer`, `peer_ip`, `check`  | gauge | Unix timestamp of the last check (`icmp` / `tcp`)                                                                |

With `--tcpcheck` but without `--tcpcheck-enforce`: `bird_rtt_keeper_tcp_alive` and TCP quality metrics reflect reality, but `bird_rtt_keeper_host_alive` and disable/enable do not account for TCP.

### Link quality (ICMP / TCP)

| Metric                                            | Labels                    | Type  | Description                                                       |
| ------------------------------------------------- | ------------------------- | ----- | ----------------------------------------------------------------- |
| `bird_rtt_keeper_icmp_packet_loss_ratio`          | `peer`, `peer_ip`         | gauge | Packet loss, % (0–100)                                            |
| `bird_rtt_keeper_icmp_rtt_seconds`                | `peer`, `peer_ip`, `stat` | gauge | RTT in seconds. `stat`: `avg`, `min`, `max`, `stddev`             |
| `bird_rtt_keeper_tcp_duration_seconds`            | `peer`, `peer_ip`, `stat` | gauge | Transfer duration in seconds. `stat`: `avg`, `min`, `max`         |
| `bird_rtt_keeper_tcp_throughput_bytes_per_second` | `peer`, `peer_ip`         | gauge | Average TCP check throughput, bytes/s                             |

### BGP / BFD (bird)

| Metric                       | Labels            | Type  | Description                                                      |
| ---------------------------- | ----------------- | ----- | ---------------------------------------------------------------- |
| `bird_bgp_session_up`        | `peer`, `peer_ip` | gauge | BGP session UP (`1`/`0`). Sync: startup + every 2 min            |
| `bird_bgp_prefixes_imported` | `peer`, `peer_ip` | gauge | Imported route count from `show protocols all`                   |
| `bird_bgp_prefixes_exported` | `peer`, `peer_ip` | gauge | Exported route count                                             |
| `bird_bfd_session_up`        | `peer`, `peer_ip` | gauge | BFD session Up. Only if session exists in `show bfd sessions`  |
| `bird_bfd_interval_seconds`  | `peer`, `peer_ip` | gauge | BFD interval from bird                                           |
| `bird_bfd_timeout_seconds`   | `peer`, `peer_ip` | gauge | BFD timeout from bird                                            |

### Info

| Metric                        | Labels                                       | Type  | Description                                                              |
| ----------------------------- | -------------------------------------------- | ----- | ------------------------------------------------------------------------ |
| `bird_rtt_keeper_peer_info`   | `peer`, `peer_ip`, `vpn`                     | gauge | Peer metadata. `vpn` from suffix `_oc` or prefix `oc_...`                |
| `bird_rtt_keeper_config_info` | `icmp_check`, `tcpcheck`, `tcpcheck_enforce` | gauge | Active check configuration for this instance                             |

### Example Prometheus scrape config

```yaml
scrape_configs:
  - job_name: bird-rtt-keeper
    static_configs:
      - targets: ['127.0.0.1:9574']
        labels:
          cluster: prod-vpn
    metric_relabel_configs:
      - source_labels: [peer]
        target_label: vpn
        regex: '.*_([^_]+)$'
        replacement: '$1'
```

### Example alerts

```yaml
groups:
  - name: bird-rtt-keeper
    rules:
      - alert: BirdBgpSessionDown
        expr: bird_bgp_session_up == 0
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "BGP session down for {{ $labels.peer }} ({{ $labels.peer_ip }})"

      - alert: BirdBfdSessionDown
        expr: bird_bfd_session_up == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "BFD session down for {{ $labels.peer }} ({{ $labels.peer_ip }})"

      - alert: BirdHostUnhealthy
        expr: bird_rtt_keeper_host_alive == 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Host health check failing for {{ $labels.peer }} ({{ $labels.peer_ip }})"

      - alert: BirdPeerDisabledByKeeper
        expr: bird_rtt_keeper_peer_enabled == 0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "Keeper disabled peer {{ $labels.peer }} ({{ $labels.peer_ip }})"

      - alert: BirdIcmpDegrading
        expr: bird_rtt_keeper_consecutive_failures{check="icmp"} >= 2
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "ICMP checks degrading for {{ $labels.peer }} ({{ $labels.peer_ip }})"

      - alert: BirdBgpPrefixesDropped
        expr: |
          (bird_bgp_prefixes_imported < bird_bgp_prefixes_imported offset 10m * 0.5)
          and bird_bgp_session_up == 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Imported prefixes dropped >50% for {{ $labels.peer }}"
```

## systemd (example)

```ini
[Unit]
Description=BIRD RTT keeper
After=bird.service
Requires=bird.service

[Service]
ExecStart=/usr/local/bin/bird-rtt-keeper
Restart=on-failure
AmbientCapabilities=CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_RAW

[Install]
WantedBy=multi-user.target
```

## Development

```bash
go test ./...
go vet ./...
```
