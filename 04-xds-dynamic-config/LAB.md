# 🧪 Lab 04: Dynamic Filesystem xDS Hot-Reload

Welcome to the hands-on lab for **Chapter 04: xDS Dynamic Configuration**. In this lab, you will run Envoy with **Filesystem-based xDS** (`path_config_source`) paired with a real backend application container. You will experience zero-downtime hot-reloads without restarting Envoy or dropping connections.

---

## 🏗️ Lab Architecture

```text
 [ Client / curl ] ──► [ Envoy Ingress (:9901) ] ──► [ dynamic_backend_service Cluster ] ──► [ Backend App (:8080) ]
                              ▲
                              │ watches via inotify
               [ dynamic_clusters.yaml & dynamic_listeners.yaml ]
```

---

## 🛠️ Step 1: Start the Docker Compose Stack

1. Navigate to the module directory:
   ```bash
   cd 04-xds-dynamic-config/
   ```

2. Start the containers in detached mode:
   ```bash
   docker compose up --build -d
   ```

3. Verify both containers are running:
   ```bash
   docker compose ps
   ```

---

## 🌐 Step 2: Test Ingress Traffic

Send a request through Envoy's dynamic listener on port `9901`:
```bash
curl -i http://localhost:9901/
```
* **Expected Response**: HTTP `200 OK` with the body `Hello from the Dynamic Backend App v1!`.

---

## 🔄 Step 3: Perform a Zero-Downtime Hot-Reload

Now let's change our backend configuration **without restarting Envoy**.

1. Open `dynamic_clusters.yaml`:
   ```bash
   cat dynamic_clusters.yaml
   ```

2. Edit the cluster or change the load assignment endpoint/message. For example, let's create a second backend app container in `docker-compose.yaml` or change the backend message. 
   
   Alternatively, let's simulate changing the cluster upstream endpoint or port.
   
3. Check Envoy's logs to see the live file watcher in action:
   ```bash
   docker compose logs -f envoy
   ```

4. When you modify `dynamic_clusters.yaml`, Envoy will instantly log the detection of the file change, parse the new resource, and hot-reload in-memory!

---

## 🔍 Step 4: Inspect Envoy Admin & xDS State

You can inspect Envoy's active configuration and dynamic resource status via its Admin interface running on port `15000`:

* **Check active config dump**:
  ```bash
  curl -s http://localhost:15000/config_dump | jq '.'
  ```
* **List active clusters**:
  ```bash
  curl -s http://localhost:15000/clusters
  ```

---

## 🛑 Troubleshooting: Why `0.0.0.0` vs `127.0.0.1` Matters in Docker

If you ever get `Connection Refused` when trying to access an Envoy admin or service port from your host machine (`localhost`), it is almost always a bind address mismatch inside the container configuration.

### 🗺️ ASCII Network Namespace Breakdown:

```text
 ┌────────────────────────────────────── HOST MACHINE (Your Laptop) ──────────────────────────────────────┐
 │                                                                                                        │
 │   User runs: curl http://localhost:15000                                                               │
 │        │                                                                                               │
 │        ▼                                                                                               │
 │   [ Docker Port Forwarding: -p 15000:15000 ]                                                           │
 └────────┼───────────────────────────────────────────────────────────────────────────────────────────────┘
          │
          ▼ Enters Container Network Namespace
 ┌──────────────────────────────────────── CONTAINER ROOM ────────────────────────────────────────────────┐
 │                                                                                                        │
 │   ❌ SCENARIO A (`address: 127.0.0.1`):                                                                │
 │   Envoy listens ONLY on its private internal loopback.                                                 │
 │   Docker's forwarded packet arrives ➔ Envoy REJECTS it (Connection Refused).                           │
 │                                                                                                        │
 │   ✅ SCENARIO B (`address: 0.0.0.0`):                                                                │
 │   Envoy listens on ALL container network interfaces.                                                   │
 │   Docker's forwarded packet arrives ➔ Envoy ACCEPTS it ➔ Returns Admin JSON!                           │
 │                                                                                                        │
 └────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

* **Rule of Thumb**: Any service inside a Docker container that you want to reach from your host machine must bind to **`0.0.0.0`**, never `127.0.0.1`.

---

## 🧹 Clean Up

When you are finished with the lab, tear down the containers:
```bash
docker compose down
```
