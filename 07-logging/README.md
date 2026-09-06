# Logging in Envoy

Logging is crucial for debugging and observability in Envoy. We distinguish between process-level logs and traffic-level logs.

## 1. Envoy Administrative Logs (Process Logs)
These logs capture the internal state of the Envoy process.
- **Configured via**: CLI flags when starting Envoy.
- **Examples**: `-l debug`, `-l info`, `--log-path /var/log/envoy/envoy.log`.
- **Purpose**: Troubleshooting startup issues, configuration reload errors, and internal server health.

## 2. Access Logs (Traffic Logs)
These logs are generated *per request/connection* and are configured within the filter chains.

### Configuration in `HttpConnectionManager`
Access logs are typically configured within the `HttpConnectionManager` (HCM).

```yaml
# Inside the http_connection_manager configuration:
access_log:
  - name: envoy.access_loggers.file
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog
      path: /dev/stdout
      # Using standard Envoy log format
      log_format:
        text_format: "[%START_TIME%] \"%REQ(:METHOD)% %REQ(X-ENVOY-ORIGINAL-PATH?:PATH)% %PROTOCOL%\" %RESPONSE_CODE% %RESPONSE_FLAGS% %BYTES_RECEIVED% %BYTES_SENT% %DURATION%ms %RESP(X-ENVOY-UPSTREAM-SERVICE-TIME)% \"%REQ(X-FORWARDED-FOR)%\" \"%REQ(USER-AGENT)%\" \"%REQ(X-REQUEST-ID)%\" \"%REQ(:AUTHORITY)%\" \"%UPSTREAM_HOST%\"\n"
```

## Key Concepts
- **Log Formatters**: Envoy supports highly customizable log formats using command operators (e.g., `%RESPONSE_CODE%`, `%START_TIME%`).
- **Access Log Service (ALS)**: For production at scale, instead of writing to files, Envoy can send access logs over gRPC to a centralized collector (like Fluentd, Datadog, or OpenTelemetry Collector).
