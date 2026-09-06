# Lab Verification & Troubleshooting

## Verification
You can verify that Envoy is correctly routing based on SNI using the `openssl s_client` tool.

### Verify Service A
```bash
openssl s_client -connect localhost:10443 -servername service-a.example.com -CAfile certs/ca.crt
```

### Verify Service B
```bash
openssl s_client -connect localhost:10443 -servername service-b.example.com -CAfile certs/ca.crt
```

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
