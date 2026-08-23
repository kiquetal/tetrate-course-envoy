# 🗄️ Envoy Redis Proxy: Active Health Checks & Routing Modes

When proxying **Redis** traffic, Envoy is not just a blind TCP forwarder. It features a native, protocol-aware **L7 Redis Proxy Filter (`envoy.filters.network.redis_proxy`)** capable of understanding commands, managing connections, and performing deep health checks.

This guide explains how to configure **Active Redis Health Checking** and details the differences between **Cluster Routing Mode** and **Pass-Through Mode** (`pass_through_mode`).

---

## 🛡️ 1. Active Redis Health Checking (PING/PONG)

A standard TCP health check only tells you if port `6379` is listening, but a Redis instance can easily be frozen, out of memory, or unresponsive while the port still accepts TCP handshakes.

Envoy solves this using the native **`envoy.health_checkers.redis`** custom health checker. It physically opens a connection, sends the Redis **`PING`** command, and expects the **`PONG`** response.

### 🛠️ Configuration Example

```yaml
clusters:
  - name: redis_backend_cluster
    connect_timeout: 0.5s
    type: STRICT_DNS
    lb_policy: MAGLEV # Standard for Redis hashing
    load_assignment:
      cluster_name: redis_backend_cluster
      endpoints:
        - lb_endpoints:
            - endpoint:
                address:
                  socket_address:
                    address: redis-master.infra.svc.cluster.local
                    port_value: 6379

    # ─── THE NATIVE REDIS HEALTH CHECK ───
    health_checks:
      - timeout: 1s
        interval: 5s
        unhealthy_threshold: 3
        healthy_threshold: 2
        custom_health_check:
          name: envoy.health_checkers.redis # ◄── Dedicated PING/PONG checker!
```

---

## 🔄 2. Redis Proxy Routing Modes: Cluster vs. Pass-Through

Under the HTTP Connection Manager counterpart for databases, the `envoy.filters.network.redis_proxy` filter governs how commands are processed. It has two primary operational states:

### ⚙️ Mode A: Command/Cluster Routing Mode (Default)
In this mode, Envoy parses **every single Redis command** sent by the client, extracts the target key, hashes it, and forwards it to the correct shard.

*   **How it works**: Envoy maintains a map of Redis cluster slots. If a query hits Envoy, it acts as the router, sending the command directly to the master node of the target slot.
*   **Best for**: Distributed **Redis Clusters** with multiple master/replica shards. It removes the burden of cluster-aware routing from your application code!

### ⚙️ Mode B: Pass-Through Mode (`pass_through_mode: true`)
When `pass_through_mode` is enabled, Envoy **disables command parsing and key-based hashing**. It acts as a lightweight, transparent protocol pipeline.

*   **How it works**: Envoy simply establishes a connection pool to the upstream cluster and passes the raw command streams straight through without looking inside or trying to route keys to specific shards.
*   **Best for**: 
    *   **Standalone Redis** instances.
    *   **Redis Sentinel** setups (or master-replica topologies where your application handles the write/read routing).
    *   Situations where you want maximum throughput and the lowest possible proxy CPU overhead.

### 🛠️ Config comparison:

#### Command/Cluster Routing (Normal):
```yaml
filter_chains:
  - filters:
      - name: envoy.filters.network.redis_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.redis_proxy.v3.RedisProxy
          stat_prefix: redis_stats
          settings:
            op_timeout: 1s
          # Envoy actively splits and routes commands to the cluster
          prefix_routes:
            catch_all_route:
              cluster: redis_backend_cluster 
```

#### Pass-Through Mode (`pass_through_mode: true`):
```yaml
filter_chains:
  - filters:
      - name: envoy.filters.network.redis_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.redis_proxy.v3.RedisProxy
          stat_prefix: redis_stats
          # ─── CRITICAL: Bypasses L7 key-parsing completely! ───
          pass_through_mode: true 
          settings:
            op_timeout: 1s
          prefix_routes:
            catch_all_route:
              cluster: redis_backend_cluster
```

---

## 📊 Summary of Differences

| Feature / Metric | Command/Cluster Routing Mode (Default) | Pass-Through Mode (`pass_through_mode: true`) |
| :--- | :--- | :--- |
| **Command Parsing** | **Yes** (Every Redis command is parsed at L7). | **No** (Raw TCP stream payload forward). |
| **Key-based Sharding**| **Yes** (Calculates hash rings/slots dynamically). | **No** (Leaves routing decisions to the client). |
| **CPU/Latency Overhead**| Slightly higher (due to parsing and slot mapping).| **Extreme low latency** (Bare-metal TCP speed). |
| **Primary Use Case** | Multi-shard Redis Cluster topologies. | Standalone instances, Redis Sentinel, or proxying. |
| **Fault Tolerance** | Can dynamically redirect queries on MOVED errors. | Pure tunnel; client must handle cluster redirects. |

---

> [!TIP]
> Use **Pass-Through Mode** if you are proxying a single standalone Redis node to keep CPU usage inside Envoy practically at zero! Switch it off and use **Cluster Routing** if you want Envoy to handle the complex key hashing of a multi-shard Redis cluster natively!
