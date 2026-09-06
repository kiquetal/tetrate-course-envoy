# SNI-Based Routing Lab

This module demonstrates how to configure Envoy to route traffic to different backend services based on the SNI (Server Name Indication) hostname provided by the client, using a single IP/port.

## 🔑 Key Concepts
1.  **`tls_inspector`**: Must be enabled in `listener_filters` to peek at the `ClientHello` and extract the SNI.
2.  **`filter_chains`**: We define multiple chains.
3.  **`filter_chain_match`**: Defines which chain to pick based on the SNI hostname (`server_names`).
4.  **`downstream_tls_context`**: Each chain can have its own TLS configuration (certificates).

## 🛠️ Generating Self-Signed Certificates

You need distinct certificates for each service:

```bash
# 1. Create CA
openssl genrsa -out ca.key 2048
openssl req -x509 -new -nodes -key ca.key -sha256 -days 365 -out ca.crt -subj "/CN=MyLocalCA"

# 2. Create Certificate for service-a
openssl genrsa -out service-a.key 2048
openssl req -new -key service-a.key -out service-a.csr -subj "/CN=service-a.example.com"
openssl x509 -req -in service-a.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out service-a.crt -days 365 -sha256

# 3. Create Certificate for service-b
openssl genrsa -out service-b.key 2048
openssl req -new -key service-b.key -out service-b.csr -subj "/CN=service-b.example.com"
openssl x509 -req -in service-b.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out service-b.crt -days 365 -sha256
```
Place these `.crt` and `.key` files in a directory accessible to Envoy (e.g., `/certs/`).

## 🧪 Verification

You can verify that Envoy is correctly routing based on SNI using the `openssl s_client` tool.

### Verify Service A
```bash
openssl s_client -connect localhost:10443 -servername service-a.example.com -CAfile ca.crt
```

### Verify Service B
```bash
openssl s_client -connect localhost:10443 -servername service-b.example.com -CAfile ca.crt
```

If configured correctly, `service-a`'s connection should present the `service-a.crt`, and `service-b`'s connection should present the `service-b.crt`.

## ⚙️ Advanced Matching: ALPN and Transport Protocols

You can further refine your routing logic by matching on `transport_protocol` (e.g., "tls") and `application_protocols` (ALPN, e.g., "h2", "http/1.1").

```yaml
filter_chains:
  # Match TLS + HTTP/2 (ALPN: h2)
  - filter_chain_match:
      transport_protocol: "tls"
      application_protocols: ["h2"]
    filters:
      - name: envoy.filters.network.http_connection_manager
        # ... HTTP/2 specific configuration ...

  # Match TLS + HTTP/1.1 (ALPN: http/1.1)
  - filter_chain_match:
      transport_protocol: "tls"
      application_protocols: ["http/1.1"]
    filters:
      - name: envoy.filters.network.http_connection_manager
        # ... HTTP/1.1 specific configuration ...
```

> **Note**: Just like SNI, you must have `envoy.filters.listener.tls_inspector` enabled to detect ALPN values. Order in `filter_chains` is crucial; Envoy matches the first chain that satisfies all criteria.
