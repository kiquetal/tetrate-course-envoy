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

## 6. Config tour (the two important blocks)

### `envoy-grpc.json` → where the config comes from

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

### `grpc-control-plane/main.go` → what gets pushed

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
