# POC Project — Tenant-Aware Edge Platform (Go + Java 21 + Envoy)

> One line: **Go owns desired state and xDS; Java 21 owns request policy and business logic; Envoy is the only thing on the traffic path.**

This document is the project brief that grows out of the Envoy labs in this repository
(`04-xds-dynamic-config/`), and a checklist of the Envoy knowledge it is meant to prove.

---

## 1. Objective

Build a small but **operable** multi-tenant edge platform that demonstrates:

1. **Zero-restart configuration**: every change (tenant, route, quota, backend) is compiled
   to an xDS snapshot and pushed over one ADS gRPC stream.
2. **A clear responsibility boundary** between a Go control plane and Java 21 services.
3. **Measured behaviour under failure**: control plane down, policy down, backend down,
   NACK loops, stale snapshots.
4. **Documented judgment**: ADRs with rejected alternatives, SLOs, runbook, cost numbers.

### Success criteria (the numbers to publish)

| Criterion | Target |
| :--- | :--- |
| Publish a new tenant | < 500 ms, 0 dropped requests |
| Config propagation to N Envoys | measured fan-out time for 1 / 10 / 100 nodes |
| Per-request overhead vs direct call | p99 delta < 2 ms |
| Backend failure (1 of 3) | 0 HTTP 5xx observed (outlier detection + retries) |
| Control plane killed | running Envoys unaffected; cold-start gap documented |
| Bad config published | NACK captured, previous snapshot still serving, alert fires |

### Non-goals

* Replacing Istio / writing a production control plane.
* Kubernetes operators (Compose is enough for the POC).
* Every Envoy filter — depth over breadth.

---

## 2. Architecture

```
                        tenants / quotas / certs (Postgres or CRDs)
                                        │
                                        ▼
        ┌───────────────────────────────────────────────────────────┐
        │  GO — control plane ("mini istiod")                       │
        │                                                           │
        │  desired state ──compile──► snapshot (in RAM, versioned)  │
        │     expand → link → dedupe → validate → version → diff    │
        │                                                           │
        │  ADS gRPC :18000      platformctl diff|apply|rollback     │
        │  NACK capture, stale-client gauge, last-good per node     │
        └───────────────────────────┬───────────────────────────────┘
                                    │ one ADS stream (CDS/EDS/RDS/LDS)
   client ──► ENVOY ────────────────┘
              │  TLS/SDS, jwt_authn, ext_authz, ratelimit,
              │  retries + retry_budget, outlier_detection, OTel
              │
              ├── ext_authz ──► JAVA 21 policy (tenant identity + headers)
              ├── RLS ───────► JAVA 21 rate limiter + Redis (global quotas)
              └── route ─────► JAVA 21 services (virtual threads)
```

### Responsibility split

| | **Go** | **Java 21** | **Envoy** |
| :--- | :--- | :--- | :--- |
| Domain | infrastructure / control | product / request policy | traffic path |
| Owns | xDS snapshots, versions, validation, diffing, ACK/NACK, CLI | business logic, authz decisions, quotas, header/body policy | routing, TLS, limits, retries, telemetry |
| Never | serves user traffic, parses HTTP bodies | builds or pushes xDS, knows snapshot internals | (nothing) — it is the boundary |
| Failure impact | Envoy keeps last snapshot; **new** Envoy cannot boot | fail-open or fail-closed — documented choice | data path is the only hard dependency |

Boundary rules: one-way config flow (Java → Go intent → Envoy), one-way request flow
(client → Envoy → Java), protobuf contracts only, no shared YAML.

---

## 3. What this project proves about Envoy (the interesting part)

These are the lessons already learned the hard way in this repo
(see [`04-xds-dynamic-config/LEARNINGS.md`](./04-xds-dynamic-config/LEARNINGS.md) and
[`GRPC-XDS.md`](./04-xds-dynamic-config/GRPC-XDS.md)), and the ones the POC must keep proving.

### 3.1 xDS is a family, and you opt in per type

| Type | Makes dynamic | Subscribed in our bootstrap |
| :--- | :--- | :--- |
| CDS | clusters (LB policy, timeouts, TLS-to-upstream) | yes |
| EDS | endpoint IPs/ports inside a cluster | no (inline `load_assignment`) |
| LDS | listeners, ports, filter chains | no (static) |
| RDS | HTTP routes (needs HCM, not `tcp_proxy`) | no |
| SDS | TLS certs / SPIFFE bundles | no |
| RTDS | runtime feature flags | no |

`ads_config` is **one pipe**; each `*_config` attached to it buys one independently
reloadable slice. Proof in the lab: the control plane only ever sees
`type.googleapis.com/envoy.config.cluster.v3.Cluster`.

### 3.2 ADS = aggregation, not a resource type

ADS is `envoy.service.discovery.v3.AggregatedDiscoveryService` — one multiplexed stream
carrying many `type_url`s. It exists because resources **depend on each other**
(listener → route → cluster → endpoint → secret) and independent streams give no ordering
guarantee; one stream lets the control plane publish bottom-up (CDS → EDS → RDS → LDS).
Per-type `version_info` still applies: ADS is one pipe, not one version.

### 3.3 "Compile" means desired state → validated, versioned protobuf artifact

No files are written. A snapshot is a `map[typeURL][]Resource` + version, held in RAM.
`cachev3.NewSnapshot` is the linker: broken cross-resource references fail *before* publish,
so a bad tenant config never reaches Envoy. Compile is a pure function → testable,
diffable, dry-runnable, rollback-able.

### 3.4 Version discipline is the whole game

`version_info` must change monotonically, or Envoy silently ignores the push. A "config
didn't apply" bug is almost always a version bug or a NACK — never a network bug.

### 3.5 NACK: the push that looks successful

Envoy rejects a snapshot on the same stream with `DiscoveryRequest.error_detail` set and
`version_info` still at the last good version → the previous config keeps serving.
Only the Go callbacks (`OnStreamRequest`) see it, so without a metric the control plane
thinks everything is fine while Envoy ignores it. Envoy side:
`control_plane.update_rejected` in `/stats`.

### 3.6 xDS is not in the data path, but it is in the boot path

Running Envoy survives a dead control plane (it caches the last applied config in RAM).
A **new** Envoy cannot start: listeners stay warming, no clusters, no traffic. That is the
real availability argument for control-plane replicas.

### 3.7 Filesystem xDS is a trap inside Docker

`path_config_source` depends on `inotify`; host kernel events do not cross Docker
bind-mounts reliably, so "hot reload" appears broken. gRPC push has no such dependency.
Rule: filesystem for natively-run Envoy, gRPC everywhere in containers.

### 3.8 Rate limiting is three systems, not one

* **Where/which** descriptors → LDS + RDS (hot-pushable).
* **How much** → the Rate Limit Service (external gRPC, `ShouldRateLimit`).
* **Counters** → RLS + Redis.
Envoy is stateless here. `local_ratelimit` = per-pod bucket (cheap, not global).
Fail-open vs fail-closed (`failure_mode_deny`) is an Envoy decision, not the RLS's.

### 3.9 The tenant is an attribute, not a deployment

One Envoy, one control plane, one policy service; tenant flows as Host/JWT claim →
`ext_authz` identity → RDS vhost → rate-limit key → metric label. Isolation ladder:
soft (vhost + quota) → medium (dedicated cluster) → hard (dedicated listener/Envoy).

### 3.10 Config JSON/YAML pitfalls worth remembering

* Proto JSON: keep `@type` first in YAML (YAML reorders keys → `could not find @type`);
  plain JSON avoids the problem entirely.
* `tcp_proxy` (L4) is the reliable minimal path; HCM + router (L7) needs correct
  `http_filters` and is where rate limits/routes actually live.
* Admin must bind `0.0.0.0` inside a container, not `127.0.0.1`.
* gRPC upstream (the control plane) needs HTTP/2:
  `typed_extension_protocol_options → explicit_http_config → http2_protocol_options`.
* `hashicorp/http-echo` listens on `:5678` by default — pass `-listen=:8080` explicitly.

### 3.11 Admin API is the debugging surface

`/server_info`, `/clusters`, `/config_dump`, `/stats`, `/listeners`, `/runtime` —
`/config_dump` shows the *applied* version (the truth), `/stats` shows `update_rejected`
and `connected_state` (the health), `/clusters` shows whether endpoints are actually up.

---

## 4. Milestones and deliverables

| # | Milestone | Deliverable |
| :--- | :--- | :--- |
| 1 | Control plane hardening | snapshot compile + validate, `/status`, version history, NACK metrics, `platformctl diff/apply/rollback` |
| 2 | L7 edge | HCM + RDS, TLS/SDS, jwt_authn, access logs, OTel traces |
| 3 | Policy + quotas | Java `ext_authz`, RLS + Redis, per-tenant SLO metrics |
| 4 | Resilience | retries/budgets, outlier detection, circuit breakers, chaos drills |
| 5 | Evidence | k6 load report (p99 overhead, fan-out), failure-drill table, 6–10 ADRs, runbook, postmortem |

### Evidence artefacts (what makes it "staff")

* **Scale numbers**: snapshot size, fan-out time, memory per Envoy at 100/1000 vhosts.
* **Failure-drill table**: expected vs observed 5xx / recovery time for each drill.
* **NACK + stale-client dashboard**: version distribution per node.
* **ADRs with rejected alternatives** (per-tenant resources vs single snapshot,
  EDS vs inline, RLS vs local, shared vs dedicated cluster, fail-open vs fail-closed).
* **One blameless postmortem** of a simulated bad publish (NACK storm, 8 min degradation).
* **Cost/capacity one-pager**: CP CPU per 1k pushes/s, RLS hop cost, Redis sizing.

---

## 5. Trade-offs to state explicitly

| Decision | Gain | Cost |
| :--- | :--- | :--- |
| xDS vs static config | no restarts, central control, audit | a stateful service to run; NACK/staleness debugging |
| Single atomic snapshot | never half-applied | whole resource re-sent per change |
| Per-tenant resources / SRDS | small blast radius | per-resource versioning, more push traffic |
| Global RLS | accurate quotas | +1 hop (~1 ms p99), Redis as stateful infra |
| Compile-before-publish | bad config never ships | publish latency, snapshot storage for rollback |
| Go + Java split | right tool per job, clean boundary | two runtimes, two build pipelines, two skill sets |

---

## 6. Current state in this repo

Working, verified labs already in place:

```bash
cd 04-xds-dynamic-config

# A. static Envoy (TCP proxy) — sanity baseline
#    curl localhost:9901/

# B. filesystem CDS (inotify caveat on Docker)
#    docker compose up -d && curl localhost:9901/

# C. gRPC ADS control plane (CDS, live push, zero restarts)
docker compose -f docker-compose.grpc.yaml up -d --build
curl -s localhost:9902/            # Hello from Backend V1 (served over gRPC xDS)
curl -s localhost:19000/switch     # push a new CDS snapshot
curl -s localhost:9902/            # Hello from Backend V2 (served over gRPC xDS)
docker inspect -f '{{.State.StartedAt}} restarts={{.RestartCount}}' xds-grpc-lab-envoy-1
```

Next step for the POC: port the Go control plane's ADS server contract into the tenant
compile pipeline, then add the Java 21 `ext_authz` + RLS pair in front of the existing
backends.
