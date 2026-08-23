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

> [!NOTE]
> By marrying **Service Discovery** (finding where hosts are) with **Active Health Checking** (confirming they are alive), Envoy ensures that your traffic is only sent to healthy, active targets, eliminating connection drops dynamically without manual operator intervention!
