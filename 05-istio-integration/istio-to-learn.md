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
By default, placing a `DestinationRule` in `namespace-b` makes it mesh-visible. The configuration is picked up by Istio's Control Plane (istiod) and pushed to all sidecars.
* **KrakenD's Envoy sidecar** in `namespace-a` downloads the configuration automatically.
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
  exportTo:
    - "*" # ◄── CRITICAL: Exports visibility to ALL namespaces (including KrakenD's namespace-a)
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
