# 🧠 gRPC xDS Control Plane Lab (the "mini istiod" in Docker)

This lab replaces the **filesystem** xDS demo (`path_config_source` in `envoy.json`)
with a **real gRPC control plane** — the same mechanism Istio, Consul, and every
production service mesh uses.

The point: **Envoy never restarts and never watches files.** A separate process
holds a gRPC stream open with Envoy and *pushes* new config when something changes.

---

## 1. The big picture

```
                        docker network: xds-grpc-net
 ┌──────────────────────────────────────────────────────────────────────────────┐
 │                                                                              │
 │   ┌────────────────────────┐            ┌──────────────────────────────┐     │
 │   │  control-plane         │            │  envoy                       │     │
 │   │  (our mini istiod)     │            │  (data plane / proxy)        │     │
 │   │                        │            │                              │     │
 │   │  ADS gRPC   :18000  ◄──┼─── gRPC ───┼──► :18000 (outbound conn)    │     │
 │   │  HTTP API   :19000  ───┼── curl ──► │                              │     │
 │   └────────────────────────┘            │  listener :9902 (static)     │     │
 │                                         │     └─ tcp_proxy             │     │
 │                                         │          └─ cluster          │     │
 │                                         │             backend_service  │     │
 │                                         │                (DYNAMIC/CDS) │     │
 │                                         └───────────────┬──────────────┘     │
 │                                                         │                    │
 │                            ┌────────────────────────────┴───────────┐        │
 │                            ▼                                        ▼        │
 │                 ┌─────────────────────┐                  ┌─────────────────────┐
 │                 │ backend-v1 :8080    │                  │ backend-v2 :8080    │
 │                 │ "Hello from V1"     │                  │ "Hello from V2"     │
 │                 └─────────────────────┘                  └─────────────────────┘
 └──────────────────────────────────────────────────────────────────────────────┘
        ▲                                                            ▲
   host :9902  (client traffic)                            host :15001 (admin)
   host :19000 (control-plane API)
```

Key detail: **the gRPC connection is initiated by Envoy, outbound to the control
plane**, and it stays open for the life of the process. That long-lived stream is
the "subscription".

---

## 2. The push sequence (what actually happens on `curl /switch`)

```
   You        control-plane (our mini istiod)              Envoy              backends
   │                      │                                  │                   │
   │ GET /switch          │                                  │                   │
   ├─────────────────────►│                                  │                   │
   │                      │ 1. build new Cluster proto       │                   │
   │                      │ 2. SetSnapshot(version="…-v2")   │                   │
   │                      │                                  │                   │
   │                      │ 3. ADS DiscoveryResponse         │                   │
   │                      │    (version_info changed)        │                   │
   │                      ├─────────────────────────────────►│                   │
   │                      │    same TCP/gRPC connection      │                   │
   │                      │    (never reconnected)           │ 4. cds: add 1     │
   │                      │                                  │    cluster(s)     │
   │                      │                                  │    remove 1       │
   │                      │                                  │                   │
   │  curl :9902          │                                  │                   │
   ├──────────────────────┼─────────────────────────────────►│ 5. tcp_proxy      │
   │                      │                                  ├──────────────────►│
   │  "Hello from V2"     │                                  │                   │
   │◄─────────────────────┼──────────────────────────────────┼───────────────────┤
   │                      │                                  │                   │
   └──────────────────────┴──────────────────────────────────┴───────────────────┘
        Nothing restarted. Envoy PID never changed. No file was touched.
```

That is exactly the flow `istiod → sidecar` uses; only the *content* differs
(real Istio also generates LDS/RDS/EDS/SDS from Kubernetes objects).

---

## 3. Files in this lab

| File | Role |
| :--- | :--- |
| `grpc-control-plane/main.go` | The xDS server: builds `Cluster` protos, serves ADS/CDS on `:18000`, exposes the toggle HTTP API on `:19000`. |
| `grpc-control-plane/Dockerfile` | Multi-stage build → tiny Alpine image with just the binary. |
| `envoy-grpc.json` | Envoy bootstrap: `ads_config` (how to reach the control plane) + static listener. **No cluster is defined here** — it comes from xDS. |
| `docker-compose.grpc.yaml` | 4 containers: `control-plane`, `envoy`, `backend-v1`, `backend-v2`. Separate project name (`xds-grpc-lab`) and ports, so it can run at the same time as the filesystem lab. |
| `envoy.json` | The older **filesystem** xDS bootstrap (kept for comparison). |

### Ports (chosen to not collide with the filesystem lab)

| Host | Container | What |
| :--- | :--- | :--- |
| `9902` | `9902` | Envoy ingress (filesystem lab uses `9901`) |
| `15001` | `15000` | Envoy admin (filesystem lab uses `15000`) |
| `19000` | `19000` | Control-plane toggle API |
| — | `18000` | ADS gRPC (internal only) |

---

## 4. Run it

```bash
cd 04-xds-dynamic-config

# build the Go control plane + start everything
docker compose -f docker-compose.grpc.yaml up -d --build

# wait ~3s, then:
curl -s http://localhost:9902/          # -> Hello from Backend V1 (served over gRPC xDS)
curl -s http://localhost:19000/status   # -> {"serving_target":"v1", ...}
```

### The magic trick (no restart, no file, no inotify)

```bash
curl -s http://localhost:19000/switch   # push v2 over ADS gRPC
curl -s http://localhost:9902/          # -> Hello from Backend V2 (served over gRPC xDS)

curl -s http://localhost:19000/switch/v1  # pin back to v1
curl -s http://localhost:9902/            # -> Hello from Backend V1 ...
```

Prove Envoy was **not** restarted:

```bash
docker inspect -f '{{.State.StartedAt}} restarts={{.RestartCount}}' xds-grpc-lab-envoy-1
```

### Watch the push happen live

```bash
# Envoy side: note "cds: add 1 cluster(s), remove 1 cluster(s)" with no startup logs before it
docker logs -f xds-grpc-lab-envoy-1 | grep --line-buffered cds

# Control-plane side: note the snapshot version + "respond open watch ... with new version"
docker logs -f xds-grpc-lab-control-plane-1
```

Check what Envoy currently holds in memory (version_info changes on every push):

```bash
curl -s http://localhost:15001/config_dump \
  | grep -o '"version_info": "[^"]*"' | head -3
```

---

## 5. Why this fixes the "hot reload doesn't work in Docker" problem

| | Filesystem xDS (`path_config_source`) | gRPC xDS (this lab) |
| :--- | :--- | :--- |
| **Trigger** | Linux `inotify` event on a file/inode | A message on an already-open TCP stream |
| **Crosses Docker bind-mount?** | ❌ Often not (host kernel event ≠ container kernel event) | ✅ N/A — no filesystem involved |
| **Who drives updates** | Envoy watches (pull) | Control plane pushes |
| **Restart needed?** | Sometimes, depending on mount/editor | **Never** |
| **Production equivalent** | Quick local experiments | Istio / Consul / Gloo / Traffic Director |

> `envoy.json` (filesystem) is fine for a single-machine demo, but the gRPC
> model is the one you will actually meet in a service mesh.

---

## 6. Config tour (the five blocks that matter)

Every byte of Envoy behaviour in this lab comes from one of five places. Two live in
`envoy-grpc.json` (bootstrap), two in `grpc-control-plane/main.go` (what gets served),
and one is implicit (the listener that *consumes* the dynamic cluster).

```
 envoy-grpc.json                                grpc-control-plane/main.go
 ├── ① dynamic_resources  (where config comes from)   ├── ④ snapshot + SetSnapshot (the push)
 ├── ② static cluster     (how to reach the ADS server)└── ⑤ makeCluster() (the payload)
 └── ③ static listener    (who consumes the cluster)
```

### ① `envoy-grpc.json` → where the config comes from

```json
"dynamic_resources": {
  "cds_config": { "ads": {}, "resource_api_version": "V3" },
  "ads_config": {
    "api_type": "GRPC",
    "transport_api_version": "V3",
    "grpc_services": [{ "envoy_grpc": { "cluster_name": "xds_control_plane" } }]
  }
}
```

* `cds_config.ads: {}` → *"get clusters from the ADS stream"* (not from a file).
* `ads_config.grpc_services` → *"the ADS server lives behind the static cluster
  `xds_control_plane`"*. That cluster is the **only** static cluster, and it must
  speak HTTP/2 (`http2_protocol_options`), because gRPC runs on HTTP/2.
* `resource_api_version: V3` → Envoy v1.30 defaults to V3 anyway; explicit is better.
* Only `cds_config` is listed → **only CDS is dynamic**. Add `lds_config`,
  `sds_config`, or HCM's `rds` to buy more reloadable slices (see `LEARNINGS.md` §8).

### ② `envoy-grpc.json` → the static cluster that reaches the control plane

This is the chicken-and-egg cluster: Envoy cannot learn how to reach the control
plane *from* the control plane, so it is static and looks like any other upstream.

```json
"clusters": [{
  "name": "xds_control_plane",
  "type": "STRICT_DNS",
  "connect_timeout": "1s",
  "load_assignment": {
    "cluster_name": "xds_control_plane",
    "endpoints": [{ "lb_endpoints": [{ "endpoint": { "address": {
      "socket_address": { "address": "control-plane", "port_value": 18000 } } } }] }]
  },
  "typed_extension_protocol_options": {
    "envoy.extensions.upstreams.http.v3.HttpProtocolOptions": {
      "@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
      "explicit_http_config": { "http2_protocol_options": {} }
    }
  }
}]
```

| Field | Why it is there |
| :--- | :--- |
| `type: STRICT_DNS` | `control-plane` is a **Docker DNS name** on `xds-grpc-net`, not an IP. Envoy resolves it and keeps endpoints in sync with DNS answers. |
| `connect_timeout: 1s` | Fail fast at boot if the control plane is not up yet (then retry with backoff). |
| `explicit_http_config.http2_protocol_options: {}` | gRPC **runs on HTTP/2**. Without it Envoy speaks HTTP/1.1 to a gRPC server and the stream never establishes. This is the #1 xDS mistake. |
| `typed_extension_protocol_options` | Modern replacement for the deprecated top-level `http2_protocol_options`. |

### ③ `envoy-grpc.json` → the static listener that consumes the dynamic cluster

```json
"listeners": [{
  "name": "ingress_listener",
  "address": { "socket_address": { "address": "0.0.0.0", "port_value": 9902 } },
  "filter_chains": [{ "filters": [{
    "name": "envoy.filters.network.tcp_proxy",
    "typed_config": {
      "@type": "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
      "stat_prefix": "tcp_proxy",
      "cluster": "backend_service"
    }
  }] }]
}]
```

The important detail: **`backend_service` does not exist in `static_resources`**. The
listener references a name that only the control plane can supply, so Envoy keeps the
listener in *warming* until CDS delivers it. That is the clearest possible proof that the
cluster is dynamic — remove the control plane from the compose file and this listener
never opens.

Reminder from `LEARNINGS.md` §10: `tcp_proxy` is L4, so there are no routes and no HTTP
filters here. Rate limiting / `ext_authz` / `jwt_authn` would require switching the
listener to `http_connection_manager`.

### ④ `grpc-control-plane/main.go` → what gets pushed (the snapshot)

```go
cachev3.NewSnapshot(version, map[resource.Type][]cachetypes.Resource{
    resource.ClusterType: {makeCluster("backend-v2")},
})
cp.snap.SetSnapshot(ctx, "global", snapshot)   // <- this pushes to all watchers
```

* A **snapshot** is the complete set of resources for one type. The version
  string must change, otherwise Envoy ignores the update.
* `constantHash` makes every Envoy node share the `"global"` snapshot (a real
  control plane hashes by `node.id` / metadata).
* `SetSnapshot` is what wakes up the open ADS stream and triggers
  `DiscoveryResponse`.
* `http.Error` on compile failure → nothing is published, so a bad snapshot can
  never partially apply.

### ⑤ `grpc-control-plane/main.go` → `makeCluster()` field by field

This function is the *entire* dynamic payload in the lab; everything else is plumbing.

```go
&cluster.Cluster{
    Name:                 "backend_service",   // ← the name the listener references
    ConnectTimeout:       durationpb.New(1 * time.Second),
    ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_STRICT_DNS},
    LbPolicy:             cluster.Cluster_ROUND_ROBIN,
    LoadAssignment: &endpoint.ClusterLoadAssignment{
        ClusterName: "backend_service",        // ← must match Name
        Endpoints: []*endpoint.LocalityLbEndpoints{{
            LbEndpoints: []*endpoint.LbEndpoint{{
                HostIdentifier: &endpoint.LbEndpoint_Endpoint{
                    Endpoint: &endpoint.Endpoint{
                        Address: &core.Address{
                            Address: &core.Address_SocketAddress{
                                SocketAddress: &core.SocketAddress{
                                    Address:       "backend-v2",  // or "backend-v1"
                                    PortSpecifier: &core.SocketAddress_PortValue{PortValue: 8080},
                                },
                            },
                        },
                    },
                },
            }},
        }},
    },
}
```

| Field | Meaning | Change it to… |
| :--- | :--- | :--- |
| `Name` | the contract with the listener's `cluster:` field | rename both sides → new wiring |
| `ConnectTimeout` | TCP connect budget to the backend | see the nested-budget rule in `LEARNINGS.md` §10 |
| `Cluster_Type` | how endpoints are discovered: `STATIC`, `STRICT_DNS`, `LOGICAL_DNS`, `EDS`, `ORIGINAL_DST` | `EDS` when endpoints come from `ClusterLoadAssignment` resources |
| `LbPolicy` | `ROUND_ROBIN`, `LEAST_REQUEST`, `RANDOM`, `RING_HASH`, `MAGLEV` | `LEAST_REQUEST` under uneven latency |
| `LoadAssignment` | inline endpoints (no EDS) | move to EDS for dynamic IPs |
| `Address: "backend-v2"` | Docker DNS name inside `xds-grpc-net` | the actual hostname/IP of the workload |

Note what is **absent**: no `tls_context` (no mTLS to the backend), no
`outlier_detection`, no `circuit_breakers`, no `health_checks`, no
`typed_extension_protocol_options` (the backend is plain HTTP/1.1 `http-echo`). Each of
those is a one-field addition and each one is a knob the POC is supposed to exercise.

### ⑥ Where to look — cheat map

| Question | File | Search for |
| :--- | :--- | :--- |
| Where does Envoy get its config? | `envoy-grpc.json` | `"dynamic_resources"` |
| Which stream carries it? | `envoy-grpc.json` | `"ads_config"` |
| How does Envoy reach the control plane? | `envoy-grpc.json` | `"xds_control_plane"` |
| Why HTTP/2? | `envoy-grpc.json` | `"explicit_http_config"` |
| Which port takes traffic? | `envoy-grpc.json` | `"ingress_listener"` / `9902` |
| Which cluster does the listener use? | `envoy-grpc.json` | `"cluster": "backend_service"` |
| What content gets pushed? | `grpc-control-plane/main.go` | `func makeCluster` |
| What triggers a push? | `grpc-control-plane/main.go` | `SetSnapshot` |
| How is a version chosen? | `grpc-control-plane/main.go` | `c.version++` |
| What can I switch at runtime? | `grpc-control-plane/main.go` | `map[string]string{"v1":` |
| Ports and containers | `docker-compose.grpc.yaml` | `ports:` |
| What Envoy *actually* applied | running Envoy | `curl :15001/config_dump` |
| Did it reject anything? | running Envoy | `curl :15001/stats \| grep control_plane` |
| What is being served now? | running control plane | `curl :19000/status` |

### ⑦ Who owns each name: `xds_control_plane` vs `backend_service`

These two strings look alike (both are `cluster_name` values) but they play opposite roles.

```
  "xds_control_plane"  =  THE PIPE   (bootstrap, local to Envoy)
  "backend_service"    =  THE TARGET (delivered BY the pipe, via CDS)
```

```bash
grep -rn "xds_control_plane" --include=*.go .     # → nothing
grep -rn "xds_control_plane" --include=*.json .
# envoy-grpc.json:25   ads_config...envoy_grpc.cluster_name
# envoy-grpc.json:59   static_resources.clusters[0].name
# envoy-grpc.json:63   that cluster's load_assignment.cluster_name

grep -rn "backend_service" --include=*.go .
# grpc-control-plane/main.go:38   clusterName = "backend_service"
grep -rn "backend_service" --include=*.json .
# envoy-grpc.json:49   tcp_proxy.cluster          ← the listener pointing at it
```

| Name | Where it is defined | Who else uses it | What it means |
| :--- | :--- | :--- | :--- |
| `xds_control_plane` | **only** `envoy-grpc.json` (3 places) | nobody — Envoy resolves it to itself | *"the upstream through which I fetch my config"* — address `control-plane:18000` |
| `backend_service` | Envoy listener **and** Go `clusterName` | both processes | *"the upstream my traffic must go to"* — the payload CDS delivers |
| `envoy-grpc-lab` (`node.id`) | `envoy-grpc.json` | sent to the server; ignored by `constantHash` | identifies the Envoy in control-plane logs |
| `global` | `grpc-control-plane/main.go` | snapshot cache key | which snapshot a node receives |

**Precisely:** `xds_control_plane` does **not** create the dynamic config. It is the static
answer to *"where do I ask for config?"* — the cable between Envoy and the control plane.
`backend_service` is the config that travels over that cable: a name with no definition in
the bootstrap, which is why the listener stays in *warming* until CDS supplies it.

Failure modes make the difference obvious:

| Typo | Result |
| :--- | :--- |
| `ads_config` points at a wrong name | Envoy **fails at boot**: `cluster 'x' not found` (the pipe is missing) |
| both `xds_control_plane` occurrences renamed consistently | **works identically** — the name is arbitrary |
| listener's `cluster` name not matched by any CDS resource | Envoy boots, listener **warms forever**, no traffic, no error in the access log |
| Go `clusterName` renamed but Envoy listener not | same as above — a silent warming, the worst failure mode |

> One line: **`xds_control_plane` = how Envoy gets its config; `backend_service` = what Envoy
> is told to use.** The first is infrastructure, the second is data.


---

## 7. Exercises (next steps)

1. **Add EDS**: move `load_assignment` out of the Cluster and into an
   `ClusterLoadAssignment` resource; serve it via `resource.EndpointType`.
   Then `cds` only sends the skeleton and `eds` sends the IPs.
2. **Add LDS**: serve the listener through xDS too, so *even the port* can change
   without restarting Envoy.
3. **Two-node snapshots**: replace `constantHash` with a real hash so
   `envoy-a` gets v1 and `envoy-b` gets v2 at the same time.
4. **Delta ADS**: set `"api_type": "DELTA_GRPC"` in `ads_config` and observe the
   incremental `DeltaDiscoveryResponse` in the control-plane logs.
5. **Kill the control plane** and watch Envoy keep serving the last snapshot
   (that is *why* xDS is safe: config is cached in memory).

---

## 8. Teardown

```bash
docker compose -f docker-compose.grpc.yaml down
```

(The filesystem lab on `9901`/`15000` is untouched — different project name,
different network, different ports.)
