# Logging in Envoy

Logging is crucial for debugging and observability in Envoy. We distinguish between process-level logs and traffic-level logs.

## 1. Envoy Administrative Logs (Process Logs)
These logs capture the internal state of the Envoy process.
- **Configured via**: CLI flags when starting Envoy.
- **Examples**: `-l debug`, `-l info`, `--log-path /var/log/envoy/envoy.log`.
- **Purpose**: Troubleshooting startup issues, configuration reload errors, and internal server health.

## 2. Access Logs (Traffic Logs)
These logs are generated *per request/connection* and are configured within the filter chains.

### Example: Configuring Access Logs in Envoy

The `access_log` configuration is placed inside the `HttpConnectionManager` (HCM). This allows Envoy to access parsed HTTP metadata (like headers, path, method) for logging.

```yaml
# Inside your http_connection_manager configuration:
access_log:
  - name: envoy.access_loggers.file
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog
      path: "/dev/stdout" # Log to standard output
      log_format:
        # Example format combining common operators and flags:
        text_format: "[%START_TIME%] \"%REQ(:METHOD)% %REQ(:PATH)%\" %RESPONSE_CODE% %RESPONSE_FLAGS% %DURATION%ms\n"
```

## Access Log Formatters

Envoy logs are highly customizable using command operators.

### Common Log Operators
- `%START_TIME%`: Timestamp of request start.
- `%REQ(:METHOD)%`: HTTP method (GET, POST, etc.).
- `%REQ(X-ENVOY-ORIGINAL-PATH?:PATH)%`: Request path.
- `%RESPONSE_CODE%`: HTTP response code (e.g., 200, 404, 503).
- `%RESPONSE_FLAGS%`: Detailed flags explaining why the request failed or was delayed (see below).
- `%BYTES_RECEIVED%`: Bytes received by Envoy.
- `%BYTES_SENT%`: Bytes sent by Envoy.
- `%DURATION%`: Total time in ms for the request to complete.
- `%RESP(X-ENVOY-UPSTREAM-SERVICE-TIME)%`: Time spent by the upstream service.
- `%UPSTREAM_HOST%`: The IP/port of the upstream backend host.

### Common Response Flags (%RESPONSE_FLAGS%)
These flags describe internal Envoy state when processing requests:

| Flag | Meaning |
| :--- | :--- |
| **UH** | **No Healthy Upstream**: No healthy hosts were found in the cluster. |
| **UF** | **Upstream Failure**: The upstream connection failed (e.g., reset, timeout). |
| **UO** | **Upstream Overflow**: The upstream circuit breaker tripped (too many requests). |
| **NR** | **No Route Configured**: No route found for the request authority/path. |
| **URX** | **Upstream Retry Limit Exceeded**: The request exceeded the maximum number of retries. |
| **NC** | **No Connection**: Envoy could not establish a connection to the upstream. |
| **DH** | **Dropped (Healtcheck)**: Request was dropped because the upstream host was being healthchecked. |
| **LH** | **Local Healthcheck Failed**: Envoy failed the healthcheck for this upstream. |
| **UT** | **Upstream Request Timeout**: The upstream request timed out. |
| **LR** | **Local Reset**: The connection was reset locally by Envoy (e.g., due to policy). |
| **UR** | **Upstream Remote Reset**: The connection was reset by the upstream server. |
| **DI** | **Delayed Injected**: The request was delayed by a fault injection policy. |
| **FI** | **Fault Injected**: The request was aborted by a fault injection policy. |
| **RL** | **Rate Limited**: The request was rejected by the rate-limit filter. |
