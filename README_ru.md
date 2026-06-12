# bird-rtt-keeper

Демон для мониторинга BGP-пиров [BIRD 2](https://bird.network.cz/) и автоматического отключения/включения протоколов при деградации канала связи.

Утилита подключается к сокету управления bird (`bird.ctl`), обнаруживает BGP-соседей, периодически проверяет их доступность по ICMP и (опционально) TCP, и при накоплении ошибок выполняет `disable` протокола в bird. После восстановления канала и истечения паузы — `enable`.

Дополнительно экспортирует метрики в формате Prometheus.

## Режимы работы

### `bird-rtt-checker` (по умолчанию)

Основной режим. Для каждого BGP-пира из bird:

1. Запускает ICMP-проверку (ping с анализом RTT и потерь).
2. Опционально — TCP-проверку (upload/download через tcpcheck-протокол на удалённой стороне).
3. При серии неудач — отключает BGP-протокол в bird (`disable <peer>`).
4. При восстановлении и истечении backoff-паузы — включает обратно (`enable <peer>`).
5. Каждые 2 минуты перечитывает список BGP-пиров и состояние BFD-сессий.
6. Отдаёт метрики на HTTP `/metrics`.

### `tcpcheck-server`

TCP-сервер для приёма проверок с удалённых хостов, где запущен `bird-rtt-checker`. Принимает соединения, выполняет upload/download тест с проверкой целостности данных. Используется peer'ами при TCP health check.

## Требования

- Go 1.24+ (для сборки из исходников)
- BIRD 2 с настроенными BGP-пирами
- Доступ к Unix-сокету bird: `/run/bird/bird.ctl` (зашит в код)
- Для ICMP-проверок: права на raw socket (`CAP_NET_RAW`) или запуск от root
- Для TCP-проверок: на стороне peer должен быть запущен `tcpcheck-server` (порт по умолчанию `32486`)

## Сборка

```bash
go build -o bird-rtt-keeper ./cmd/
```

Бинарник из CI/release называется `bird-rtt-peers-checker`.

## Запуск

### Основной режим

```bash
./bird-rtt-keeper
```

Минимальный пример с отключением TCP-проверок:

```bash
./bird-rtt-keeper --tcpcheck=false
```

Строгий режим — TCP-проверка влияет на отключение пира:

```bash
./bird-rtt-keeper --tcpcheck-enforce
```

### TCP-сервер (на стороне peer)

```bash
./bird-rtt-keeper --mode=tcpcheck-server --ports=32486
```

Несколько портов:

```bash
./bird-rtt-keeper --mode=tcpcheck-server --ports=32486,32487
```

## Опции командной строки


| Флаг                 | По умолчанию       | Описание                                                                             |
| -------------------- | ------------------ | ------------------------------------------------------------------------------------ |
| `--mode`             | `bird-rtt-checker` | Режим: `bird-rtt-checker` или `tcpcheck-server`                                      |
| `--ports`            | `32486`            | Список TCP-портов через запятую (только для `tcpcheck-server`)                       |
| `--tcpcheck`         | `true`             | Включить TCP health check для BGP-пиров                                              |
| `--tcpcheck-enforce` | `false`            | Учитывать результат TCP-проверки при отключении/включении пира. Требует `--tcpcheck` |
| `--metrics`          | `true`             | Включить Prometheus-экспортёр                                                        |
| `--metrics-listen`   | `127.0.0.1:9574`   | Адрес HTTP-сервера метрик                                                            |


## Переменные среды


| Переменная | Где используется  | Описание                                                                               |
| ---------- | ----------------- | -------------------------------------------------------------------------------------- |
| `PORTS`    | `tcpcheck-server` | Список портов через запятую. Используется только если флаг `--ports` не задан или пуст |


Для основного режима `bird-rtt-checker` переменные среды **не используются**. Путь к сокету bird (`/run/bird/bird.ctl`) задаётся в коде.

## Логика проверок

### ICMP

Каждый цикл — 28 пакетов, интервал 2 с, таймаут 58 с. Проверка считается неудачной, если:


| Критерий         | Порог    |
| ---------------- | -------- |
| Потери пакетов   | > 20%    |
| Средний RTT      | > 200 ms |
| Максимальный RTT | > 800 ms |
| Минимальный RTT  | > 100 ms |
| StdDev RTT       | > 80 ms  |


### TCP

Интервал между проверками: 5 минут + jitter 30–270 с. Для каждой проверки — 2 попытки download и 2 upload по 10 MB (таймаут 20 с). Проверка неудачна, если:


| Критерий                          | Порог  |
| --------------------------------- | ------ |
| Доля ошибок                       | > 20%  |
| Средняя длительность              | > 12 с |
| Максимальная длительность         | > 18 с |
| Минимальная длительность          | > 12 с |
| StdDev длительности (≥ 5 попыток) | > 7 с  |


Порт подключения: `32486` (зашит в `TcpChecker`).

### Отключение и включение пира


| Проверка                       | Подряд неудач для disable | Подряд успехов для сброса паузы |
| ------------------------------ | ------------------------- | ------------------------------- |
| ICMP                           | 3                         | 8                               |
| TCP (при `--tcpcheck-enforce`) | 2                         | 4                               |


- При disable: `birdc disable <peer>`, начальная пауза **150 с**, при повторном disable пауза удваивается.
- Re-enable возможен только после истечения текущей паузы и при успешных проверках.
- Пауза сбрасывается до 0, если с момента последнего disable прошло ≥ 45 мин и накоплено достаточно успешных проверок.

При `--tcpcheck` без `--tcpcheck-enforce` TCP-проверка выполняется и попадает в метрику `bird_rtt_keeper_tcp_alive`, но **не влияет** на disable/enable и на `bird_rtt_keeper_host_alive`.

## Метрики Prometheus

Эндпоинт: `http://<metrics-listen>/metrics` (по умолчанию `http://127.0.0.1:9574/metrics`).

Общие labels для большинства метрик: `peer` (имя BGP-протокола в bird), `peer_ip` (IP соседа). При удалении пира time series удаляются.

### Health check (keeper)


| Метрика                                    | Labels                      | Тип   | Описание                                                                                                           |
| ------------------------------------------ | --------------------------- | ----- | ------------------------------------------------------------------------------------------------------------------ |
| `bird_rtt_keeper_host_alive`               | `peer`, `peer_ip`           | gauge | Хост проходит все **включённые** проверки (`1`/`0`). ICMP всегда; TCP — только при `--tcpcheck --tcpcheck-enforce` |
| `bird_rtt_keeper_icmp_alive`               | `peer`, `peer_ip`           | gauge | Последняя ICMP-проверка успешна                                                                                    |
| `bird_rtt_keeper_tcp_alive`                | `peer`, `peer_ip`           | gauge | Последняя TCP-проверка успешна. Только при `--tcpcheck`                                                            |
| `bird_rtt_keeper_peer_enabled`             | `peer`, `peer_ip`           | gauge | Keeper не отключил протокол в bird (`1` = enabled)                                                                 |
| `bird_rtt_keeper_pause_remaining_seconds`  | `peer`, `peer_ip`           | gauge | Секунд до возможного re-enable после disable                                                                       |
| `bird_rtt_keeper_consecutive_failures`     | `peer`, `peer_ip`, `check`  | gauge | Подряд неудачных проверок. `check`: `icmp` или `tcp` (tcp — реальный результат, независимо от enforce)             |
| `bird_rtt_keeper_consecutive_successes`    | `peer`, `peer_ip`, `check`  | gauge | Подряд успешных проверок                                                                                           |
| `bird_rtt_keeper_last_disable_reason_info` | `peer`, `peer_ip`, `reason` | gauge | Причина последнего disable (значение всегда `1`). `reason=none` если disable не было                               |
| `bird_rtt_keeper_last_check_timestamp`     | `peer`, `peer_ip`, `check`  | gauge | Unix timestamp последней проверки (`icmp` / `tcp`)                                                                 |

При `--tcpcheck` без `--tcpcheck-enforce`: `bird_rtt_keeper_tcp_alive` и TCP-метрики качества отражают реальность, но `bird_rtt_keeper_host_alive` и disable/enable TCP не учитывают.         

### Качество канала (ICMP / TCP)


| Метрика                                           | Labels                    | Тип   | Описание                                                      |
| ------------------------------------------------- | ------------------------- | ----- | ------------------------------------------------------------- |
| `bird_rtt_keeper_icmp_packet_loss_ratio`          | `peer`, `peer_ip`         | gauge | Потери пакетов, % (0–100)                                     |
| `bird_rtt_keeper_icmp_rtt_seconds`                | `peer`, `peer_ip`, `stat` | gauge | RTT в секундах. `stat`: `avg`, `min`, `max`, `stddev`         |
| `bird_rtt_keeper_tcp_duration_seconds`            | `peer`, `peer_ip`, `stat` | gauge | Длительность transfer в секундах. `stat`: `avg`, `min`, `max` |
| `bird_rtt_keeper_tcp_throughput_bytes_per_second` | `peer`, `peer_ip`         | gauge | Средняя пропускная способность TCP-проверки, байт/с           |


### BGP / BFD (bird)


| Метрика                      | Labels            | Тип   | Описание                                                     |
| ---------------------------- | ----------------- | ----- | ------------------------------------------------------------ |
| `bird_bgp_session_up`        | `peer`, `peer_ip` | gauge | BGP-сессия UP (`1`/`0`). Sync: старт + каждые 2 мин          |
| `bird_bgp_prefixes_imported` | `peer`, `peer_ip` | gauge | Число imported routes из `show protocols all`                |
| `bird_bgp_prefixes_exported` | `peer`, `peer_ip` | gauge | Число exported routes                                        |
| `bird_bfd_session_up`        | `peer`, `peer_ip` | gauge | BFD-сессия Up. Только если сессия есть в `show bfd sessions` |
| `bird_bfd_interval_seconds`  | `peer`, `peer_ip` | gauge | BFD interval из bird                                         |
| `bird_bfd_timeout_seconds`   | `peer`, `peer_ip` | gauge | BFD timeout из bird       

### Info 


| Метрика                       | Labels                                              | Тип   | Описание                                                                 |
| ----------------------------- | --------------------------------------------------- | ----- | ------------------------------------------------------------------------ |
| `bird_rtt_keeper_peer_info`   | `peer`, `peer_ip`, `vpn`                            | gauge | Метаданные пира. `vpn` из суффикса `_oc` или префикса `oc_...`           |
| `bird_rtt_keeper_config_info` | `icmp_check`, `tcpcheck`, `tcpcheck_enforce`        | gauge | Активная конфигурация проверок на данном instance                        |


### Пример scrape-конфига Prometheus

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

### Пример алертов

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

## systemd (пример)

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

## Разработка

```bash
go test ./...
go vet ./...
```

