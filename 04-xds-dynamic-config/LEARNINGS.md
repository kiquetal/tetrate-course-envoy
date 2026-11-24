# 🧠 Chapter 04 Learnings & Troubleshooting Log

This document records key learnings, pitfalls, and debugging breakthroughs encountered while building and testing Envoy sidecar architectures in Chapter 04.

---

## 🔑 1. The Protobuf JSON Rule: `@type` Must Be First
When configuring Envoy via JSON or YAML, `typed_config` represents a polymorphic `google.protobuf.Any` type. 

* **The Pitfall**: Envoy's underlying C++ Protobuf JSON parser reads objects sequentially. If fields like `stat_prefix` or `cluster` appear *before* `@type` inside `typed_config`, Envoy cannot determine the filter schema and crashes at startup with:
  ```text
  Unable to parse JSON as proto (INVALID_ARGUMENT: could not find @type ...)
  ```
* **The Solution**: Always use pure JSON configuration files (`envoy.json`) or ensure that `@type` is explicitly placed as the **very first key** inside every `typed_config` block.

---

## 🌐 2. Docker Network Isolation & The Sidecar Pattern
When running Envoy alongside an application container in Docker Compose:

```text
 ┌────────────────────────────────────────────────────────────────────────┐
 │ HOST MACHINE (Your Laptop)                                             │
 │                                                                        │
 │   curl http://localhost:10000/                                         │
 └───────┬────────────────────────────────────────────────────────────────┘
         │
         ▼ 1. Enters via Port Mapping (-p 10000:10000)
 ┌───────┴────────────────────────────────────────────────────────────────┐
 │ DOCKER BRIDGE NETWORK (`mesh-net`)                                     │
 │                                                                        │
 │   ┌────────────────────────────────┐         2. TCP Proxy Traffic      ┌────────────────────┐
 │   │ Envoy Container (Sidecar)      │ ───────────────────────────────►  │ App Container      │
 │   │                                │         (Hostname: `app`)         │                    │
 │   │  - Listens on 0.0.0.0:10000    │                                   │ - Listens on :8080 │
 │   │  - Admin UI on :15000          │                                   │ - Backend Echo     │
 │   └────────────────────────────────┘                                   └────────────────────┘
 │                                                                        │
 │   🔒 Backend App is NOT exposed to your host laptop (Protected!)       │
 └────────────────────────────────────────────────────────────────────────┘
```

* **`expose:` vs `ports:`**:
  * **`expose:`** makes the port available to other containers on the Docker network, but keeps it hidden from your host laptop.
  * **`ports:`** forwards the port out to your host laptop so you can test it with `curl`.
* **Zero Trust Security**: By exposing only Envoy and keeping the backend app sealed inside the internal Docker network, external clients are forced to pass through Envoy's filters, routing, and access logs.

---

## 🥋 3. The Envoy Admin API "Jedi Cheat Sheet"

To master Envoy troubleshooting, you need to know which admin endpoint to query. Here is your go-to reference mapping each admin URL to its real-world SRE equivalent:

| Admin Endpoint | Jedi Equivalent / Analogy | What it tells you |
| :--- | :--- | :--- |
| **`/server_info`** | 🪪 **The ID Card & Medical Chart** | Envoy process version, build info, uptime, and operational state (`LIVE`). |
| **`/clusters`** | 📞 **The Upstream Phone Directory** | All backend service clusters, their health status, and live member endpoints (IPs/ports). |
| **`/config_dump`** | 🧠 **The Full Memory Dump (X-Ray)** | The complete active runtime configuration (listeners, routes, clusters, secrets) in JSON. |
| **`/stats`** | 📊 **The Dashboard / Speedometer** | Live counters and gauges (request counts, 5xx error rates, active connections, latency). |
| **`/listeners`** | 🚪 **The Inbound Doorbell Registry** | All active ports and network listeners listening for downstream traffic. |
| **`/runtime`** | ⚙️ **The Feature Flag Panel** | Dynamic runtime configuration layers and feature flags currently active. |

---

## 🧭 4. The Golden Rule: Downstream vs. Upstream (From Envoy's POV)

The biggest point of confusion in Envoy is perspective. **The POV is ALWAYS Envoy.** 

```text
 ┌───────────────────┐               ┌───────────────────┐               ┌───────────────────┐
 │ Downstream Client │               │    ENVOY PROXY    │               │  Upstream Server  │
 │ (Browser / App)   │               │    (THE POV)      │               │ (Database / App)  │
 └─────────┬─────────┘               └─────────┬─────────┘               └─────────┬─────────┘
           │                                   │                                   │
           │ 1. Initiates connection           │                                   │
           │    TO Envoy (Requests coming IN)  │                                   │
           ├──────────────────────────────────►│                                   │
           │                                   │ 2. Initiates connection           │
           │                                   │    TO backend (Requests going OUT)│
           │                                   │──────────────────────────────────►│
           │                                   │                                   │
           │    👉 DOWNSTREAM                  │    👉 UPSTREAM                    │
```

* **Downstream (Where traffic comes *FROM*)**:
  > Any client that **dials into Envoy**, initiates a TCP connection, or sends an HTTP request (e.g., a web browser, mobile app, or another mesh service calling your app).
* **Upstream (Where traffic is sent *TO*)**:
  > The backend destination that **Envoy dials out to** to forward requests and fetch responses (e.g., your local Go/Node container on `localhost:8080`, external databases, or third-party APIs).

---

## 🎛️ 5. The xDS Dynamic Parts Reference Guide

Here is the quick reference guide for all the dynamic components of Envoy (xDS), what they control, and where to find their examples in this repository:

| Discovery Service | Acronym | What it controls | Example in this Repo |
| :--- | :--- | :--- | :--- |
| **Listener Discovery Service** | **LDS** | Inbound ports, IP bindings, and network/HTTP filter stacks. | `04-xds-dynamic-config/dynamic_listeners.json` |
| **Route Discovery Service** | **RDS** | L7 routing tables (virtual host matching, path prefix rules). | Embedded in HCM route configs |
| **Cluster Discovery Service** | **CDS** | Upstream backend services (timeouts, load balancing policies). | `04-xds-dynamic-config/dynamic_clusters.json` |
| **Endpoint Discovery Service** | **EDS** | Live Pod/Task IP addresses and ports inside a Cluster. | Streamed dynamically via control plane / SDS |
| **Secret Discovery Service** | **SDS** | Dynamic TLS certificates, private keys, and SPIFFE trust bundles. | `01-bootstrap/` (SPIRE UDS integration) |

---

## 🔌 6. Filesystem xDS vs gRPC xDS (what actually works in Docker)

This was the biggest practical lesson of the chapter. Same Envoy, same version, two completely different update mechanisms:

| | `path_config_source` (filesystem) | `ads_config` + gRPC (control plane) |
| :--- | :--- | :--- |
| Trigger | Linux `inotify` (file write / inode change) | Message on an already-open gRPC stream |
| Crosses a Docker bind-mount | ❌ often not (host kernel event ≠ container kernel event) | ✅ no filesystem involved |
| Who drives the update | Envoy polls/watches (pull) | Control plane pushes |
| Restart needed | Sometimes (depends on mount / editor save strategy) | **Never** |
| Real-world equivalent | quick single-machine experiments | Istio / Consul / Gloo / Traffic Director |

Rule of thumb learned the hard way: **inside Docker, use gRPC xDS; use the filesystem only when Envoy runs natively.**

The working demo lives in `grpc-control-plane/` (Go + `envoyproxy/go-control-plane`) and `docker-compose.grpc.yaml`. Full walkthrough with the push sequence, port map and exercises: [`GRPC-XDS.md`](./GRPC-XDS.md).

```bash
cd 04-xds-dynamic-config
docker compose -f docker-compose.grpc.yaml up -d --build

curl -s localhost:9902/            # Hello from Backend V1 (served over gRPC xDS)
curl -s localhost:19000/switch     # push a NEW CDS snapshot over ADS gRPC
curl -s localhost:9902/            # Hello from Backend V2 (served over gRPC xDS)

docker inspect -f '{{.State.StartedAt}} restarts={{.RestartCount}}' xds-grpc-lab-envoy-1
```

Look for this in `docker logs xds-grpc-lab-envoy-1` — no startup logs before it, just the delta:

```text
[info][upstream] cds: add 1 cluster(s), remove 1 cluster(s)
[info][upstream] cds: added/updated 1 cluster(s), skipped 0 unmodified cluster(s)
```

That log line pair **is** the proof that the whole "dynamic" story in Envoy is real: config changed, connections kept, process untouched.

---

## 🧠 7. What `/switch` actually changes (the in-memory map)

`/switch` touches **no file**. It rewrites one entry inside the control plane's RAM and wakes up the gRPC stream that is already open.

```
  CONTROL PLANE PROCESS (xds-grpc-lab-control-plane-1)
 ┌───────────────────────────────────────────────────────────────────────────┐
 │  RAM                                                                      │
 │                                                                           │
 │  (a) targets map — YOUR routing decision (plain Go map, in the code)      │
 │      ┌──────────────────────────────┐                                     │
 │      │ "v1" -> "backend-v1"         │   /switch picks a key here          │
 │      │ "v2" -> "backend-v2"         │                                     │
 │      └──────────────────────────────┘                                     │
 │                    │ makeCluster(addr)                                    │
 │                    ▼                                                      │
 │  (b) snapshot cache — go-control-plane state (library memory)             │
 │      ┌─────────────────────────────────────────────────────┐              │
 │      │ key "global"                                        │              │
 │      │   version_info : "20261124-020438-v2"  ← must change │              │
 │      │   CDS          : [ Cluster{backend_service,          │              │
 │      │                     STRICT_DNS, load_assignment} ]   │              │
 │      └─────────────────────────────────────────────────────┘              │
 │                    │ SetSnapshot(...)  = "publish"                        │
 └────────────────────┼──────────────────────────────────────────────────────┘
                      │ long-lived gRPC stream (ADS), opened BY Envoy
 ┌────────────────────▼──────────────────────────────────────────────────────┐
 │  ENVOY PROCESS (xds-grpc-lab-envoy-1)                                     │
 │   RAM: cluster manager -> backend_service:8080   ← replaced in place      │
 │        listener :9902 (static, never touched)                             │
 │        tcp_proxy  -> cluster "backend_service"                            │
 └────────────────────┬──────────────────────────────────────────────────────┘
                      │ TCP
              ┌───────┴────────┐
              ▼                ▼
       backend-v1:8080   backend-v2:8080
```

The physical steps of one `curl /switch`:

```
 1. read current target from map (a)        -> "v1"
 2. choose the other one                    -> "v2"
 3. addr = targets["v2"]                    -> "backend-v2"
 4. version++ (a NEW version string is mandatory, else Envoy ignores it)
 5. build a brand-new Cluster proto pointing at backend-v2
 6. snapshotCache.SetSnapshot("global", snap)   ◄── THE UPDATE (RAM only)
 7. SetSnapshot wakes the open watch on that key
        │
        ▼  DiscoveryResponse{version_info: "...-v2", resources:[Cluster]}
        │        (same TCP connection, no reconnect)
        ▼
 Envoy: version differs -> apply -> ACK
        logs: cds: add 1 cluster(s), remove 1 cluster(s)
```

The 3 lines of Go that *are* the reload:

```go
c.version++
snapshot, _ := cachev3.NewSnapshot(version, map[resource.Type][]cachetypes.Resource{
    resource.ClusterType: {makeCluster(addr)},   // full desired state, not a diff
})
c.snap.SetSnapshot(ctx, "global", snapshot)      // <- push happens here
```

Why the state must live in the server's memory:

* a **new** Envoy connects → "give me CDS" → server answers from RAM, no recomputation
* Envoy **reconnects** → same answer, same version, still from RAM
* control plane **dies** → Envoy keeps serving its own last applied config (also in RAM)

Three copies of the truth, all observable:

```bash
curl -s localhost:19000/status                                         # server: what it serves now
curl -s localhost:15001/config_dump | grep -o '"version_info": "[^"]*"' # envoy: what it applied
docker logs xds-grpc-lab-control-plane-1                               # the push itself
```

---

## 🧩 8. What is dynamic in this lab? Only CDS

This is the part that is easy to get wrong: "dynamic configuration" is not one switch. Each xDS type covers a different slice of the config, and **you opt into each one separately** in the bootstrap.

```
 envoy-grpc.json (bootstrap, read once at startup, never dynamic)
 ├── node                : id "envoy-grpc-lab"
 ├── admin               : :15000                       [STATIC]
 ├── dynamic_resources
 │     ├── cds_config -> ads:{} ......................... [DYNAMIC ✅ CDS]
 │     └── ads_config -> grpc_services -> cluster xds_control_plane
 └── static_resources
       ├── listeners[0]  : :9902 + tcp_proxy ............ [STATIC ❌ never changes]
       └── clusters[0]   : xds_control_plane -> :18000 .. [STATIC ❌ needed to reach the CP]

 what the control plane serves (the only moving part):
   Cluster "backend_service"
     ├── type          : STRICT_DNS
     ├── lb_policy     : ROUND_ROBIN
     ├── connect_timeout: 1s
     └── load_assignment: backend-v1|v2:8080  (endpoints INLINE, so no EDS)
```

| xDS type | What it would make dynamic | Dynamic here? | Why |
| :--- | :--- | :--- | :--- |
| **CDS** | clusters: timeouts, LB policy, TLS to upstream, endpoint type | ✅ **yes** | `cds_config: { ads: {} }` |
| **EDS** | the endpoint list (IP:port) *inside* a cluster | ❌ no | endpoints are inline in `load_assignment` |
| **LDS** | listeners: ports, filter chains, `tcp_proxy` / HCM | ❌ no | listener lives in `static_resources` |
| **RDS** | HTTP route table (needs HCM; irrelevant for `tcp_proxy`) | ❌ no | TCP proxy has no routes |
| **SDS** | TLS certs / keys / SPIFFE bundles | ❌ no | no TLS in this lab |
| **RTDS** | runtime feature flags (`runtime_layer`) | ❌ no | not configured |

Proof, straight from the processes:

```text
control-plane log   -> only "type.googleapis.com/envoy.config.cluster.v3.Cluster" ever requested
envoy config_dump   -> ClustersConfigDump (dynamic) vs ListenersConfigDump (static)
```

### How to make each one dynamic (the menu)

**EDS** — split the endpoints out of the cluster:

```json
"cluster": { "name": "backend_service", "type": "EDS",
             "eds_cluster_config": { "eds_config": { "ads": {}, "resource_api_version": "V3" } } }
```
then the control plane also serves `resource.EndpointType` (`ClusterLoadAssignment`).
Benefit: change IPs/ports without re-sending the whole cluster.

**LDS** — delete the listener from `static_resources` and add `"lds_config": { "ads": {} }`;
the control plane starts serving `Listener` protos. Then even the ingress **port** changes live.
(Keep a static listener locally only if you must bootstrap the admin/first listener.)

**RDS** — only with `http_connection_manager`: HCM gets
`"rds": { "route_config_name": "local_route", "config_source": { "ads": {} } }`, and you serve
`RouteConfiguration`. This is where path-based routing lives (Istio VirtualService).

**SDS** — serve `Secret` protos (cert chain + key) instead of `tls_certificates` on disk;
this is what makes cert rotation possible without restarting sidecars (Istio/SPIRE).

**RTDS** — serve `Runtime` protos to flip feature flags (e.g. `envoy.reloadable_features.*`)
at runtime.

### One-line rule

> `ads_config` is **one pipe**; every `*_config` entry you point at it (`cds_config`, `lds_config`,
> `rds` inside HCM, `eds_cluster_config`, `sds_config`) buys you **one independently reloadable
> slice** of the config. This lab only bought CDS — that's why `cds:` is the only thing that
> ever appears in the logs.




