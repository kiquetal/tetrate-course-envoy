# Lab Verification & Troubleshooting

## Testing ALPN-based Routing

You can test ALPN routing by passing the `-alpn` flag to `openssl`.

### Verify HTTP/2 (h2)
```bash
openssl s_client -connect localhost:10443 -servername service-a.example.com -alpn h2 -CAfile certs/ca.crt
```
*   **Expected**: Look for `ALPN protocol: h2` in the output.

### Verify HTTP/1.1
```bash
openssl s_client -connect localhost:10443 -servername service-a.example.com -alpn http/1.1 -CAfile certs/ca.crt
```
*   **Expected**: Look for `ALPN protocol: http/1.1` in the output.

## Troubleshooting: "Failed to load incomplete private key"
If you see this error, it is almost always a permission issue inside the Docker container, as the `envoy` user needs read access to the certificates.

### Fix Permissions
Run this command from the `06-sni-routing/` directory to make the files readable by the container:

```bash
chmod 644 certs/*.key certs/*.crt
```
After changing permissions, restart the environment:
```bash
docker-compose down
docker-compose up
```
