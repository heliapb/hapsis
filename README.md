# Hapsis

**Hapsis** (from Ancient Greek *hapsís*, “arch, vault”) is the singular form of apsis, one of the two extreme points of an orbit.

Hapsis is a federating proxy for [Grafana Tempo](https://github.com/grafana/tempo), in the spirit of [promxy](https://github.com/jacksontj/promxy) and [lokxy](https://github.com/paulojmdias/lokxy).

## Status

[WIP] only implemented  **Trace by ID** (`/api/v2/traces/{id}`) by now.

## Run

```shell
go build ./cmd/hapsis
./hapsis -config config.example.yaml
curl -s localhost:3200/api/v2/traces/<trace id>
```

## Demo

A Docker Compose demo runs two Tempos, each holding part of the same trace, behind hapsis, plus Grafana:

```shell
make demo
curl -s localhost:3200/api/v2/traces/0123456789abcdef0123456789abcdef
```

The response contains spans from both Tempos. Grafana is at http://localhost:3000 (Explore, "hapsis" datasource, trace ID above). Tear down with `make demo-down`.

## License

[AGPL-3.0-only](LICENSE)
