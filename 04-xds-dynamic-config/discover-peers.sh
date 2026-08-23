#!/bin/bash
# FILE: 04-xds-dynamic-config/discover-peers.sh
set -euo pipefail

PEER_DNS="peer-service.proteus.local"
TARGET_PATH="/tmp/dynamic_peers.yaml"
TMP_PATH="${TARGET_PATH}.tmp"

echo "[Proteus Agent] Starting dynamic peer discovery loop for: $PEER_DNS"

while true; do
  # 1. Resolve peer IPs from AWS DNS / Cloud Map
  # (Using dig +short. If DNS returns empty, fallback to a safe loop)
  IPS=$(dig +short "$PEER_DNS" | grep -E '^[0-9.]+$' || true)

  if [ -z "$IPS" ]; then
    echo "[Proteus Agent] WARNING: No active peer IPs discovered. Retrying..."
    sleep 10
    continue
  fi

  # 2. Build the Envoy v3 Dynamic Cluster Resource YAML in memory
  {
    echo "resources:"
    echo "  - \"@type\": type.googleapis.com/envoy.config.cluster.v3.Cluster"
    echo "    name: peer_service_cluster"
    echo "    connect_timeout: 0.25s"
    echo "    type: STATIC"
    echo "    lb_policy: ROUND_ROBIN"
    echo "    load_assignment:"
    echo "      cluster_name: peer_service_cluster"
    echo "      endpoints:"
    echo "        - lb_endpoints:"
    
    for IP in $IPS; do
      echo "            - endpoint:"
      echo "                address:"
      echo "                  socket_address:"
      echo "                    address: $IP"
      echo "                    port_value: 9902" # Peer's secure mTLS ingress port
    done
  } > "$TMP_PATH"

  # 3. ATOMIC SWAP: Rename temp file to target file (atomic on POSIX filesystems)
  mv "$TMP_PATH" "$TARGET_PATH"

  echo "[Proteus Agent] Updated active peer endpoints: $(echo $IPS | tr '\n' ' ')"
  
  sleep 10
done
