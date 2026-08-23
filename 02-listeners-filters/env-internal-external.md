# 🛡️ Envoy Security: How Envoy Determines Internal vs. External Requests

In modern cloud architectures, determining whether a request originated from **inside** the corporate network/service mesh or from the **public internet** is critical for security:
*   **Internal requests** are granted higher trust (e.g., authorization to call sensitive APIs, bypassing secondary authentications, propagating internal headers like `x-envoy-internal`).
*   **External requests** are untrusted (untrusted headers must be scrubbed or overwritten to prevent header spoofing).

This guide answers the core security question:

> **Which piece of information does Envoy use to determine whether a request is internal or external, and why is the "Trusted Client Address" the definitive answer?**

---

## 🧭 The Core Engine: `use_remote_address` & Connection IP

At a basic level, Envoy’s **HTTP Connection Manager (HCM)** inspects the raw Layer 4 downstream connection:

1.  **Immediate Downstream IP**: By default, Envoy looks at the physical IP address of the TCP connection that just connected to its listener.
2.  **`internal_address_config` Match**: Envoy compares this IP against its configured list of internal CIDR ranges (by default, standard RFC1918 private networks: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`).
    *   If the connection IP is in the private ranges ➔ Envoy marks the request as **Internal** and sets `x-envoy-internal: true`.
    *   Otherwise ➔ The request is marked **External**.

---

## 🚨 The Edge Proxy Problem (Why Connection IP Fails)

In production, Envoy rarely receives direct connections from public clients. Instead, it sits behind an **Edge Load Balancer** (like AWS ALB, Cloudflare, or a reverse proxy):

```text
[ Public Client ] ──► [ Edge Load Balancer ] ──► [ Envoy Proxy ]
 (IP: 203.0.113.5)       (IP: 10.0.1.50)           (Receives L4 TCP from 10.0.1.50)
```

If Envoy only looked at the physical TCP connection IP:
*   Every connection arrives from `10.0.1.50` (which is inside the RFC1918 private network!).
*   Envoy would treat **100% of public internet traffic as trusted, internal requests!** This is a severe security vulnerability.

Conversely, we cannot simply trust the client's `X-Forwarded-For` (XFF) header blindly, because a malicious public client can easily spoof it by adding a header like `X-Forwarded-For: 10.0.0.5` before sending the request.

---

## 🏆 The Definitive Solution: The "Trusted Client Address"

To solve this vulnerability, Envoy uses the **Trusted Client Address** as the definitive source of truth.

The **Trusted Client Address** is the IP address that Envoy calculates *after* traversing a configured number of trusted edge proxies inside the `X-Forwarded-For` header.

### ⚙️ How Envoy Calculates the Trusted Client Address:

To configure this in the HTTP Connection Manager, you use two critical parameters:
1.  **`use_remote_address: true`**: Tells Envoy to treat the downstream remote address (the physical TCP IP) as a trusted connection.
2.  **`xff_num_trusted_hops: N`**: Tells Envoy exactly how many reverse proxies (hops) in front of it are trusted.

```yaml
# configuration inside envoy.yaml (HCM)
typed_config:
  "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
  use_remote_address: true
  xff_num_trusted_hops: 1 # ◄── Trust exactly 1 hop (the Edge Load Balancer)
```

### 🔍 Walking the Header (Step-by-Step Security Resolution)

When a request arrives at Envoy, the header looks like this:
```text
X-Forwarded-For: 198.51.100.10 (Spoofed), 203.0.113.5 (Real Client)
Remote TCP Connection IP: 10.0.1.50 (ALB)
```
*(Note: Because the real client connected to the ALB, the ALB automatically appended the real client's IP `203.0.113.5` to the far right of the incoming `X-Forwarded-For` header list before routing it to Envoy).*

With `xff_num_trusted_hops: 1`, Envoy processes the address list **from right to left**:

1.  **Hop 0 (TCP Remote IP)**: `10.0.1.50` (The ALB) ➔ **Trusted**.
2.  **Hop 1 (1st entry in XFF from the right)**: **`203.0.113.5`** (The Real Client) ➔ **Trusted**.

Because `xff_num_trusted_hops` is configured to exactly `1`, Envoy's trust boundary stops at **Hop 1**. 

Therefore, **`203.0.113.5`** is extracted directly as the **Trusted Client Address**! Envoy completely ignores anything to the left of Hop 1 (such as the spoofed `198.51.100.10`), rendering the spoofing attempt harmless.

---

## 🏁 The Final Verdict: Internal vs. External

Once the **Trusted Client Address** is determined:

1.  Envoy compares the **Trusted Client Address** (`203.0.113.5`) against the `internal_address_config` (private CIDRs).
2.  Since `203.0.113.5` is a public IP and does **not** fall inside the private networks, Envoy marks the request as **External**.
3.  **Header Sanitization**: Because it is marked external, Envoy **scrubs/overwrites** the spoofed headers (such as `x-envoy-internal` or internal-only custom headers), securing your backend service mesh completely!

> [!IMPORTANT]
> The **Trusted Client Address** is the definitive answer because it is the only IP address guaranteed to represent the real client, safely resolved by stripping away trusted reverse-proxy hops while preventing header spoofing from untrusted public clients.
