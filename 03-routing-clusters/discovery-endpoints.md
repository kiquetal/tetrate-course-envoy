# 🌐 Service Discovery, Clusters, Endpoints & Health Checking in Envoy

In Envoy, routing decisions eventually resolve to sending traffic to a physical server. This process is governed by the relationship between **Clusters**, **Endpoints**, **Service Discovery**, and **Health Checking**.

This guide breaks down these critical concepts, tracing how they operate in raw Envoy configurations and how they are abstracted in **Istio**.

---

## 🧱 The Core Hierarchy: Cluster ➔ Endpoint

In Envoy, traffic flows down a structured pipeline, where a **Cluster** represents a logical grouping, and the **Endpoints** are the physical, individual backend targets:

```text
[ Client Request ] ──► [ Listener ] ──► [ Route Match ]
                                              │
                                              ▼
                    +───────────────────────────────────────────────────+
                    | CLUSTER: "payment-service"                        |
                    | (Defines Load Balancer & Health Check Policies)   |
                    |                                                   |
                    |   +───────────────────────────────────────────+   |
                    |   | ENDPOINT pool (Collection of instances)   |   |
                    |   |                                           |   |
                    |   |   [ Endpoint A: 10.244.0.10:8080 ]        |   |
                    |   |     ▲                                     |   |
                    |   |     │ Health Check: OK (200)              |   |
                    |   |     │ Route Traffic: YES ─────────────────┼───┼──► [ Active Server A ]
                    |   |                                           |   |
                    |   |   [ Endpoint B: 10.244.0.11:8080 ]        |   |
                    |   |     ▲                                     |   |
                    |   |     │ Health Check: OK (200)              |   |
                    |   |     │ Route Traffic: YES ─────────────────┼───┼──► [ Active Server B ]
                    |   |                                           |   |
                    |   |   [ Endpoint C: 10.244.0.12:8080 ]        |   |
                    |   |     ▲                                     |   |
                    |   |     │ Health Check: FAILED (503)          |   |
                    |   |     │ Route Traffic: NO (EJECTED)         |   |
                    |   |                                           |   |
                    |   +───────▲───────────────────────────────────+   |
                    +───────────┼───────────────────────────────────────+
                                │
                        [ Active Probing ]
                        Envoy periodically sends L7 requests
                        (e.g. GET /healthz) to all endpoints!
```

*   **Cluster**: A logical grouping of upstream hosts/services that perform identical duties (e.g., `payment-service`). The cluster defines connection timeouts, load balancing algorithms (like Round Robin), TLS credentials, and health checking rules.
*   **Endpoint**: The physical network location of a service instance. This is represented by an **IP address and port** (e.g. `10.244.0.15:8080`) or a **DNS Hostname** (e.g., `payment-db.internal`).

---

## 🔍 Service Discovery Modes (How Envoy Finds Endpoints)

Envoy does not need endpoints hardcoded in a static file. It supports several powerful **Service Discovery Modes** configured via the `type` field in the Cluster:

### 1. `STATIC` (Hardcoded)
Endpoints are explicitly listed in the `envoy.yaml` configuration. Used for local testing or unchanging external APIs.

### 2. `STRICT_DNS` (Periodic Resolution)
Envoy periodically queries a DNS server (like AWS DNS at `169.254.169.253` or Kubernetes CoreDNS). It takes **all resolved IP addresses** and treats them as the active pool of endpoints.
*   **Best for**: Scaling services backend by standard DNS names (e.g. a Kubernetes Service DNS).

### 3. `LOGICAL_DNS` (Dynamic Single-Connection)
Similar to `STRICT_DNS`, but instead of keeping a list of all IPs, Envoy resolves the hostname **only when establishing a new connection**, holding on to the first resolved IP.
*   **Best for**: Massive-scale external endpoints (e.g. calling `api.stripe.com` or `dynamodb.us-east-1.amazonaws.com`) where a single hostname might map to hundreds of dynamically changing IPs.

### 4. `EDS` (Endpoint Discovery Service - Dynamic xDS)
The absolute standard in service meshes. Envoy does not query DNS or read files. Instead, a control plane (like **Istio**) opens a persistent gRPC stream to Envoy and pushes endpoint IPs/ports dynamically in real-time as pods scale up or down.
*   **Best for**: Dynamic container environments (Kubernetes).

---

## 🛠️ Raw Envoy Configuration Template: Clusters, DNS Endpoints & Health Checks

Here is a fully complete `envoy.yaml` cluster block showing how to configure **`STRICT_DNS`** with a hostname endpoint and active **HTTP Health Checking**:

```yaml
clusters:
  - name: secure_payment_cluster
    connect_timeout: 0.25s
    
    # ─── 1. SERVICE DISCOVERY MODE ───
    type: STRICT_DNS
    dns_lookup_family: V4_ONLY
    
    # ─── 2. LOAD BALANCING POLICY ───
    lb_policy: ROUND_ROBIN
    
    # ─── 3. ENDPOINT DEFINITION (Using Hostname instead of IP) ───
    load_assignment:
      cluster_name: secure_payment_cluster
      endpoints:
        - lb_endpoints:
            - endpoint:
                address:
                  socket_address:
                    address: payment-api.infra.svc.cluster.local # ◄── DNS Hostname resolved periodically!
                    port_value: 443
                    
    # ─── 4. ACTIVE HEALTH CHECKING CONFIGURATION ───
    health_checks:
      - timeout: 1s
        interval: 5s
        unhealthy_threshold: 3   # 3 failed checks mark host as UNHEALTHY (removed from LB)
        healthy_threshold: 2     # 2 passed checks mark host as HEALTHY (restored to LB)
        http_health_check:
          path: "/healthz"       # Endpoint evaluated inside the target app
          expected_statuses:
            - start: 200         # Expected status range 200-299
              end: 300
```

---

## 🔍 Active vs. Passive Health Checking (Outlier Detection)

In Envoy, health checking falls into two distinct categories: **Active** and **Passive**.

| Feature | Active Health Checking (`health_checks`) | Passive Health Checking (`outlier_detection`) |
| :--- | :--- | :--- |
| **Mechanics** | Envoy **proactively** sends artificial probe requests (e.g. `GET /healthz`) periodically. | Envoy **passively** monitors real application traffic flowing to the endpoints. |
| **Network Overhead**| Higher (probe packets are sent continuously even if there is no client traffic). | **Zero** (no synthetic packets are created; it uses real client request statistics). |
| **Vulnerability** | Probes might succeed while actual real user requests are failing (due to subtle database/routing issues). | 100% accurate to the real user experience. |
| **Action** | Removes host from the pool completely until health probes pass again. | **Ejects** the host from the load balancing pool temporarily for a cooling-off period. |

### 🛠️ Raw Envoy Configuration for Passive Health Checking (Outlier Detection)

In Envoy, Passive Health Checking is configured using the **`outlier_detection`** block inside the cluster. It maps perfectly to modern **v3 API** standards:

```yaml
clusters:
  - name: secure_payment_cluster
    connect_timeout: 0.25s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: secure_payment_cluster
      endpoints:
        - lb_endpoints:
            - endpoint:
                address:
                  socket_address:
                    address: payment-api.infra.svc.cluster.local
                    port_value: 443

    # ─── PASSIVE HEALTH CHECKING (OUTLIER DETECTION) ───
    outlier_detection:
      consecutive_5xx: 3          # Eject host after 3 consecutive 5xx server errors
      interval: 10s               # How often Envoy analyzes statistics
      base_ejection_time: 30s     # How long the host is ejected (cooling-off period)
      max_ejection_percent: 100   # Max percentage of hosts that can be ejected (allows ejecting all)
      consecutive_gateway_failure: 5 # Eject on connection/gateway timeout errors
```

---

## ☸️ The Istio Map: Connecting Envoy to Istio Abstractions

When you move to **Istio**, you do not write raw Cluster configs. Instead, Istio translates high-level Kubernetes and Istio resources into these exact Envoy concepts under the hood:

### 1. `Service` (Kubernetes Core) ➔ `STRICT_DNS` or `EDS`
When you define a standard Kubernetes service, Istio automatically reads the API server:
* If standard sidecars are used, Istio builds an **`EDS`** cluster pushing Pod IP endpoints directly into Envoy's sidecar memory.
* If it targets an external service, it builds a **`STRICT_DNS`** cluster in Envoy.

### 2. `ServiceEntry` (Istio) ➔ Endpoint Address Hostname
If you want to route to a service outside the mesh (like an external payment gateway or database hostname), you deploy an Istio **`ServiceEntry`**. 
This tells Istiod to generate an Envoy cluster targeting that specific hostname:

```yaml
apiVersion: networking.istio.io/v1alpha3
kind: ServiceEntry
metadata:
  name: external-payment-gateway
  namespace: default
spec:
  hosts:
    - api.external-payment.com
  ports:
    - number: 443
      name: https
      protocol: HTTPS
  resolution: DNS # ◄── Translates to STRICT_DNS inside Envoy's cluster config!
```

### 3. `DestinationRule` (Istio) ➔ Health Checking
Active health checking and outlier detection (passive health checking) are configured via the Istio **`DestinationRule`**:

```yaml
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
  name: payment-outlier-detection
spec:
  host: payment-api.infra.svc.cluster.local
  trafficPolicy:
    outlierDetection: # ◄── Translates to Envoy's dynamic health checking & host ejection!
      consecutive5xxErrors: 3
      interval: 10s
      baseEjectionTime: 30s
      maxEjectionPercent: 100
```

---

## ⚖️ 3. Load Balancing Algorithms (`lb_policy`)

Once Envoy has a pool of healthy endpoints, it must decide *which* specific instance receives the incoming request. Envoy supports several highly optimized **Load Balancing Policies** defined via `lb_policy` in the cluster:

*   **`ROUND_ROBIN` (Default)**: Alternates requests across all healthy endpoints sequentially. Best for stateless, evenly loaded workloads.
*   **`LEAST_REQUEST`**: Tracks active concurrent requests and routes the next query to the host with the **fewest outstanding connections**. Excellent for varying database queries or tasks with unpredictable processing times.
*   **`RANDOM`**: Randomly picks an endpoint.
*   **`RING_HASH`**: Consistent hashing based on request parameters (like headers or cookies). It maps keys to a virtual ring of endpoints, ensuring the same client or key always hits the same backend instance.
*   **`MAGLEV`**: Google's high-performance consistent hashing algorithm. Faster than `RING_HASH` lookup and generates more uniform key distributions, making it the industry standard for Redis/Memcached proxying.

---

## ⚡ 4. Circuit Breakers (Connection & Request Pool Limits)

Unlike Outlier Detection (which ejects failed hosts *after* errors happen), **Circuit Breaking in Envoy acts as a proactive gatekeeper**. It sets strict limits on connection pools to prevent a sudden spike in traffic from causing a cascade failure across your upstream cluster.

If these limits are crossed, Envoy immediately drops subsequent requests with a `503` locally (bypassing the network entirely to protect the overloaded backend).

### ⚙️ Raw Envoy v3 Configuration Example:
```yaml
clusters:
  - name: protected_db_cluster
    connect_timeout: 0.25s
    type: STRICT_DNS
    lb_policy: LEAST_REQUEST
    
    # ─── THE GATEKEEPER: CIRCUIT BREAKERS ───
    circuit_breakers:
      thresholds:
        - priority: DEFAULT
          max_connections: 1024       # Max L4 TCP connections Envoy will open to the backend
          max_requests: 512           # Max concurrent L7 requests outstanding (HTTP/2 / HTTP/3)
          max_pending_requests: 100   # Max requests queued in memory waiting for a connection slot
          max_retries: 3              # Max concurrent retries allowed mesh-wide (prevents "retry storms")
```

---

## 🔌 L4 TCP vs. L7 HTTP Connection Pools & The Istio Protocol Upgrade Paradox

When configuring Circuit Breakers, it is vital to distinguish between transport-level (L4) and application-level (L7) connection pools. This distinction becomes critical in an **Istio Service Mesh** because of **Automatic Protocol Upgrades**.

### 📊 The Core Difference

*   **TCP Connection Pool (Layer 4 - Transport)**:
    *   Manages raw **physical TCP sockets** opened between Envoy and the backend. It does not inspect the contents of the stream.
    *   *Primary Limit*: `max_connections` (Limits physical socket handshakes).
    *   *Primary Use Case*: Databases (MySQL/PostgreSQL/Redis) and HTTP/1.1 without client-side multiplexing.
*   **HTTP Connection Pool (Layer 7 - Application)**:
    *   Manages **concurrent HTTP streams/requests** multiplexed *inside* those physical TCP connections.
    *   *Primary Limits*: `max_requests` (Outstanding active requests) and `max_pending_requests` (Queue size).
    *   *Primary Use Case*: HTTP/2, HTTP/3, and gRPC.

---

### ⚠️ The Istio Upgrade Paradox: Why `max_connections` Fails as a Shield

In a default Kubernetes setup, an HTTP/1.1 application requires a separate TCP connection for every concurrent request, meaning setting `max_connections` acts as an effective shield.

**However, inside an Istio Service Mesh, this changes completely:**

1.  **Automatic HTTP/2 Upgrade**: When your application sends standard **HTTP/1.1** traffic, the client sidecar intercepts it. If the destination is also in the mesh, **Istio automatically upgrades the over-the-wire connection to HTTP/2 (or mTLS with ALPN `istio-h2`)**!
2.  **The Multiplexing Tunnel**: Instead of opening 100 raw TCP sockets, the sidecars **multiplex all 100 concurrent HTTP requests over a single, long-lived TCP connection**!
3.  **The Flaw**: If you configure a circuit breaker with `max_connections: 5`, **it will fail to protect your service**. Because Istio has collapsed the transport layer down to `1` or `2` TCP connections, a client can still blast **10,000 concurrent HTTP requests** inside those few open TCP connections, completely crashing your backend app!

### 🛡️ SRE Best Practice: Use L7 Limits to Limit Upgraded Services

When running inside Istio, you **must use L7 limits** (`max_requests` or `maxRequestsPerConnection`) inside your connection pools to restrict concurrent load:

*   Keep `max_connections` low or default (since Istio multiplexes everything anyway to save socket overhead).
*   Set **`maxRequestsPerConnection`** and **`http1MaxPendingRequests`** to protect the actual container processing threads from getting overwhelmed!

---

## ☸️ The Istio Map: Connecting to DestinationRule

Both **Load Balancing** and **Circuit Breaking** are mapped directly into the Istio **`DestinationRule`** under `trafficPolicy`:

```yaml
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
  name: protected-service-policy
spec:
  host: my-app.default.svc.cluster.local
  trafficPolicy:
    # ─── LOAD BALANCING MAP ───
    loadBalancer:
      simple: LEAST_REQUEST # ◄── Maps to Envoy's lb_policy: LEAST_REQUEST
      
    # ─── CIRCUIT BREAKERS MAP ───
    connectionPool:
      tcp:
        maxConnections: 1024 # ◄── Maps to max_connections
      http:
        http1MaxPendingRequests: 100 # ◄── Maps to max_pending_requests
        maxRequestsPerConnection: 10 # (Limits load per socket)
```

---

> [!NOTE]
> By combining **Service Discovery** (finding endpoints), **Health Checks / Outlier Detection** (verifying readiness), **Load Balancing** (equalizing traffic), and **Circuit Breaking** (pool limits), Envoy provides a complete, resilient self-healing network boundary that protects both client request stability and server safety!
