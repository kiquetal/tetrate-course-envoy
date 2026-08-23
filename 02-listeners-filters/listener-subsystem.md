# 🎧 The Listener Subsystem: Proxy Protocol, TLS Inspector & HTTP Inspector

In Envoy, the **Listener Subsystem** is the absolute entry point for all incoming connections. 

Before a TCP connection is handed over to a **Network Filter Chain** (like the HTTP Connection Manager), it passes through sequential **Listener Filters**. These filters operate on the raw socket *before* the connection is formally accepted or decrypted. They parse initial bytes to dynamically route traffic, extract client metadata, or sniff protocols.

This guide covers the three most powerful listener filters studied in the course:
1. **Proxy Protocol Filter** (`envoy.filters.listener.proxy_protocol`)
2. **TLS Inspector** (`envoy.filters.listener.tls_inspector`)
3. **HTTP Inspector** (`envoy.filters.listener.http_inspector`)

---

## 🗺️ The Architecture: Where Listener Filters Sit

Listener filters run in sequence **before** network filter chains are matched:

```text
Incoming TCP Socket
       │
       ▼
 [ LISTENER FILTERS ]
 ┌──────────────────────────────────────────────┐
 │ 1. Proxy Protocol Filter (Reads client IP)    │
 └──────────────────────┬───────────────────────┘
                        ▼
 ┌──────────────────────────────────────────────┐
 │ 2. TLS Inspector (Sniffs SNI / ALPN)         │
 └──────────────────────┬───────────────────────┘
                        ▼
 ┌──────────────────────────────────────────────┐
 │ 3. HTTP Inspector (Sniffs HTTP version)      │
 └──────────────────────┬───────────────────────┘
                        ▼
 [ FILTER CHAIN MATCH ] ➔ Dynamically routes to the correct Network Filter!
```

---

## 🛠️ 1. Proxy Protocol Filter (`envoy.filters.listener.proxy_protocol`)

### 🧠 The Problem:
When Envoy sits behind an L4 Load Balancer (like an **AWS Network Load Balancer (NLB)** or HAProxy), the load balancer terminates the client's TCP socket and opens a new TCP socket to Envoy. 
* As a result, Envoy sees the **Load Balancer's private IP** as the client address, completely losing the real client's original IP and port!

### 💡 The Solution:
The **Proxy Protocol** (v1 or v2) prepends a tiny header containing the real client's IP and port to the very beginning of the TCP stream before forwarding it to Envoy.
The **Proxy Protocol Filter** intercepts this header, extracts the client IP, and overrides Envoy's downstream connection metadata with the **true client IP**!

---

## 🔒 2. TLS Inspector Filter (`envoy.filters.listener.tls_inspector`)

### 🧠 The Problem:
You want to host multiple domain names (e.g. `api.proteus.local` and `dashboard.proteus.local`) on the **same physical ingress port (e.g. 443)**, but each domain uses a different SSL/TLS certificate. Or you want to route encrypted TLS traffic directly to an upstream server **without decrypting it** in Envoy (TLS passthrough).

### 💡 The Solution:
The **TLS Inspector** parses the initial bytes of the incoming TLS **Client Hello** handshake. It extracts:
* **SNI (Server Name Indication)**: The domain name the client is trying to reach.
* **ALPN (Application-Layer Protocol Negotiation)**: The protocol the client wants to use (e.g., `h2`, `http/1.1`).

This metadata is attached to the connection context, allowing Envoy to dynamically match the connection to the correct `filter_chains` block using `filter_chain_match`!

---

## 🌐 3. HTTP Inspector Filter (`envoy.filters.listener.http_inspector`)

### 🧠 The Problem:
You want to support both plaintext HTTP/1.1 and plaintext HTTP/2 traffic on the **same physical port**, or you want to auto-detect whether incoming traffic is raw TCP vs HTTP without requiring separate ingress ports.

### 💡 The Solution:
The **HTTP Inspector** sniffs the first few bytes of the incoming plaintext connection. 
* If it sees standard HTTP/1.1 headers (e.g. `GET / HTTP/1.1`), it marks the connection as HTTP/1.1.
* If it sees the HTTP/2 connection preface (`PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n`), it marks the connection as HTTP/2.

This allows Envoy to dynamically route HTTP traffic to the HTTP Connection Manager and raw TCP traffic to a TCP Proxy filter chain on the same port!

---

## ⚙️ Complete Envoy v3 Configuration Example: Port Unification & Protocol Detection

Here is a complete, production-grade `envoy.yaml` showing how to combine all three filters to achieve **Port Unification** on port `8443`. 

This listener will:
1. Extract the client IP via the **Proxy Protocol**.
2. Sniff the SNI domain via the **TLS Inspector**.
3. Dynamically route to **mTLS/HTTP** if the SNI is `api.proteus.local`, or route to a **Raw TCP Proxy** if the SNI is `db.proteus.local`!

```yaml
# FILE: 02-listeners-filters/listener-subsystem.md
static_resources:
  listeners:
    - name: unified_ingress_listener
      address:
        socket_address:
          address: 0.0.0.0
          port_value: 8443

      # ─── 🎧 1. THE LISTENER FILTERS CHAIN (Runs sequentially on raw socket) ───
      listener_filters:
        # Step A: Parse and strip the Proxy Protocol header to extract true client IP
        - name: envoy.filters.listener.proxy_protocol
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.filters.listener.proxy_protocol.v3.ProxyProtocol

        # Step B: Sniff the SNI / ALPN from TLS Client Hello without decrypting yet
        - name: envoy.filters.listener.tls_inspector
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.filters.listener.tls_inspector.v3.TlsInspector

        # Step C: Sniff plaintext HTTP version if the connection is not TLS
        - name: envoy.filters.listener.http_inspector
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.filters.listener.http_inspector.v3.HttpInspector

      # ─── 🔀 2. DYNAMIC FILTER CHAIN MATCHING (Based on sniffed metadata) ───
      filter_chains:
        
        # ────────── CHAIN A: SECURE HTTP ROUTING (For API Traffic) ──────────
        - filter_chain_match:
            server_names: ["api.proteus.local"] # ◄── Matched via TLS Inspector SNI!
            transport_protocol: "tls"           # Only match TLS connections
          filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: api_ingress
                route_config:
                  name: api_routes
                  virtual_hosts:
                    - name: api_service
                      domains: ["*"]
                      routes:
                        - match: { prefix: "/" }
                          route: { cluster: local_api_cluster }
                http_filters:
                  - name: envoy.extensions.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.http.router.v3.Router

        # ────────── CHAIN B: SECURE TCP PASSTHROUGH (For DB Traffic) ──────────
        - filter_chain_match:
            server_names: ["db.proteus.local"]  # ◄── Matched via TLS Inspector SNI!
            transport_protocol: "tls"
          filters:
            - name: envoy.filters.network.tcp_proxy
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
                stat_prefix: db_passthrough
                cluster: secure_database_cluster # Routes raw encrypted TLS bytes directly to DB!
```

---

> [!TIP]
> **Performance Impact**: Listener filters are highly optimized C++ components. Running the `tls_inspector` or `http_inspector` introduces virtually **zero CPU overhead** because they only inspect the first few packets (the handshake/preface bytes) and then detach, leaving the rest of the high-throughput streaming directly to the kernel and the selected network filter!
