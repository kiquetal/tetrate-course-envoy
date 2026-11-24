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



