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

