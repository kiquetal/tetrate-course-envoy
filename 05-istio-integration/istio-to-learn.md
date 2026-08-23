# ☸️ Istio Cross-Namespace Architecture: DestinationRule & Circuit Breaking

When orchestrating microservices with API Gateways (like **KrakenD**) and **Istio Service Mesh**, developers frequently face namespace boundary decisions. This guide answers the core architectural question:

> **"If my API Gateway (KrakenD) and target backend services are in different namespaces, where should I deploy the Istio `DestinationRule` for Circuit Breaking—in the source (invoker) namespace or target (service) namespace?"**

---

## 🏁 The Best Practice: The Target Namespace

Always place the `DestinationRule` (defining connection pools, circuit breakers, and outlier detection) in the **Target Namespace (where the destination Service resides)**.

### 🎨 The Cross-Namespace Flow (Visualized)

Imagine **KrakenD** is running in `namespace-a` (the invoker) and your **Go Backend** is running in `namespace-b` (the target):

```text
[ namespace-a ] (Invoker / KrakenD)    [ namespace-b ] (Target / Service Owner)
┌───────────────────────────┐         ┌──────────────────────────────────────────────┐
│  KrakenD Pod              │         │  Go Backend Pod                              │
│  ┌─────────────────────┐  │         │  ┌───────────────────┐                       │
│  │   KrakenD Gateway   │  │         │  │    Go App         │                       │
│  └──────────┬──────────┘  │         │  └─────────▲─────────┘                       │
│             │ (localhost) │         │            │ (localhost)                     │
│  ┌──────────▼──────────┐  │         │  ┌─────────┴─────────┐                       │
│  │   Envoy Sidecar     │  │         │  │   Envoy Sidecar   │                       │
│  │   (Active Client)   │◄─┼─────────┼─►│   (Active Server) │                       │
│  └──────────┬──────────┘  │  mTLS   │  └───────────────────┘                       │
└─────────────┼─────────────┘  TCP    └──────────────────────────────────────────────┘
              │                                      ▲
              │ 2. Envoy applies Circuit Breaker     │ 1. Applies DR configuration
              │    rules locally at egress!          │    cross-namespace dynamically
              ▼                                      │
       ┌─────────────────────────────────────────────┴┐
       │   DestinationRule (CRD)                      │
       │   - namespace: namespace-b (Target)          │
       │   - host: go-app.namespace-b.svc.cluster.local│
       │   - exportTo: ["*"] (Visible to everyone)    │
       └──────────────────────────────────────────────┘
```

---

## 🧠 Core Architectural Reasons

### 1. Service Ownership & Domain Boundaries
The team developing and operating the backend service in `namespace-b` knows its resource capabilities:
* They know how many concurrent database connections the app can scale to.
* They understand at what point a container is considered overloaded.
* **Placing the rule in their namespace** ensures they own the lifecycle of the service's capacity limits.

### 2. Egress Enforcement with Dynamic Visibility (`exportTo`)
By default, placing a `DestinationRule` in `namespace-b` makes it globally mesh-visible. In modern Istio (1.10+), if the `exportTo` field is omitted, it **automatically defaults to `*` (all namespaces)**.
* **KrakenD's Envoy sidecar** in `namespace-a` downloads the configuration automatically from the control plane (istiod).
* When KrakenD initiates a call to `go-app.namespace-b.svc.cluster.local`, the **egress sidecar inside KrakenD's pod** intercepts the traffic and cuts the connection locally *before* wasting network packets traversing to the destination!

### 3. Preventing Configuration Duplication & Drift
If you placed the `DestinationRule` in the source namespace (`namespace-a`), and tomorrow a new frontend service in `namespace-c` also calls the backend, you would have to duplicate the `DestinationRule` inside `namespace-c`. 
This creates synchronization issues and config drift. Centrally locating it in `namespace-b` creates a **single source of truth** for all mesh clients.

---

## 🛠️ The Production-Grade DestinationRule Manifest

Here is the exact Kubernetes manifest setup. Placing it in `namespace-b` with `exportTo: ["*"]` ensures KrakenD can read and enforce it perfectly:

```yaml
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
  name: go-backend-circuit-breaker
  namespace: namespace-b # ◄── Target namespace where your backend service resides
spec:
  host: go-app.namespace-b.svc.cluster.local # FQDN of the target service
  # exportTo: ["*"] # ◄── OPTIONAL in modern Istio! Defaults to "*" (all namespaces) if omitted.
  trafficPolicy:
    connectionPool:
      tcp:
        maxConnections: 100 # Max TCP connections before circuit opens (L4)
      http:
        http1MaxPendingRequests: 10 # Max queued requests waiting for a connection (L7)
        maxRequestsPerConnection: 10 # Max requests per connection (L7)
    outlierDetection:
      consecutive5xxErrors: 3 # Break circuit if 3 consecutive 5xx errors occur
      interval: 10s
      baseEjectionTime: 30s
      maxEjectionPercent: 100 # Allow ejecting all unhealthy hosts
```

---

## 🌐 Global Rate Limiting in Istio via `EnvoyFilter`

Since Istio's standard high-level resources (`VirtualService`, `Gateway`) do **not** have fields for global rate limiting, Istio exposes a highly powerful escape hatch: the **`EnvoyFilter`** Custom Resource. 

An `EnvoyFilter` allows you to patch the underlying Envoy configurations inside the sidecar side-by-side with Istio's generated configs.

To configure Global Rate Limiting in Istio, you deploy an `EnvoyFilter` that does **two patches**:
1. **The L7 Filter Patch**: Injects the `envoy.filters.http.ratelimit` filter into the sequential filter chain.
2. **The Route Action Patch**: Defines the dynamic header extraction (descriptors) on a specific route match.

### 🛠️ The Global Rate Limit `EnvoyFilter` Manifest

Here is the exact production-grade manifest you deploy to `namespace-b` to enforce global rate limits on your Go Backend:

```yaml
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: go-backend-rate-limit
  namespace: namespace-b # Same namespace as the target service
spec:
  workloadSelector:
    labels:
      app: go-app # ◄── Tells Istio to only patch Envoy sidecars inside your Go App pods
  configPatches:
    # ────────────────────────────────────────────────────────────────
    # PATCH 1: Inject the L7 Rate Limit Filter into http_filters
    # ────────────────────────────────────────────────────────────────
    - applyTo: HTTP_FILTER
      match:
        context: SIDECAR_INBOUND # Patch inbound sidecar traffic
        listener:
          filterChain:
            filter:
              name: "envoy.filters.network.http_connection_manager"
              subFilter:
                name: "envoy.filters.http.router" # Match the terminal router filter
      patch:
        operation: INSERT_BEFORE # Insert BEFORE the router terminates the pipeline!
        value:
          name: envoy.filters.http.ratelimit
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit
            domain: go_app_limits
            rate_limit_service:
              grpc_service:
                envoy_grpc:
                  # Tells Envoy to dial the RLS service running in your cluster
                  cluster_name: outbound|8081||ratelimit-service.infra.svc.cluster.local
              transport_api_version: V3

    # ────────────────────────────────────────────────────────────────
    # PATCH 2: Inject the Rate Limit Actions/Descriptors into virtual_hosts
    # ────────────────────────────────────────────────────────────────
    - applyTo: HTTP_ROUTE
      match:
        context: SIDECAR_INBOUND
        routeConfiguration:
          vhost:
            name: "inbound|http|8080" # Match the inbound Go App port virtual host
            route:
              action: ANY
      patch:
        operation: MERGE
        value:
          route:
            rate_limits:
              - actions:
                  - request_headers:
                      header_name: ":method"
                      descriptor_key: "http_method"
```

### 🧠 How it Works in Istio:
1. **Dynamically Patched**: When Istiod detects this `EnvoyFilter`, it automatically regenerates the configuration for the `go-app` Envoy sidecars.
2. **Insert Before Router**: In **PATCH 1**, we tell Envoy to insert the Rate Limit filter `INSERT_BEFORE` the `router` subFilter. This preserves the absolute law of Envoy: **the router must always terminate the chain!**
3. **gRPC Cluster String**: In **PATCH 1**, the `cluster_name` uses Istio's standard outbound cluster naming convention (`outbound|<port>||<FQDN>`), routing the gRPC check cleanly through the mesh backbones!

