// A minimal Envoy xDS control plane for learning purposes.
//
// It serves the Cluster Discovery Service (CDS) over the Aggregated
// Discovery Service (ADS) gRPC stream, and exposes a tiny HTTP API so you can
// flip the upstream between backend-v1 and backend-v2 without restarting
// Envoy:
//
//	curl -s localhost:19000/switch     # toggle v1 <-> v2
//	curl -s localhost:19000/status     # see what is currently served
package main

import (
	"context"
	"encoding/json"
	"flag"
	stdlog "log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	clustersvc "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	endpointsvc "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	cachetypes "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resource "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	clusterName  = "backend_service"
	backendPort  = 8080
	nodeHashKey  = "global" // every Envoy node shares the same snapshot
	initialPoint = "v1"
)

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

type stdLogger struct{}

func (stdLogger) Debugf(format string, args ...interface{}) { stdlog.Printf("[debug] "+format, args...) }
func (stdLogger) Infof(format string, args ...interface{})  { stdlog.Printf("[info]  "+format, args...) }
func (stdLogger) Warnf(format string, args ...interface{})  { stdlog.Printf("[warn]  "+format, args...) }
func (stdLogger) Errorf(format string, args ...interface{}) { stdlog.Printf("[error] "+format, args...) }

// constantHash makes every Envoy node with any node.id receive the same
// snapshot. In production you would hash by node.id / metadata instead.
type constantHash struct{}

func (constantHash) ID(node *core.Node) string { return nodeHashKey }

// ---------------------------------------------------------------------------
// xDS resources
// ---------------------------------------------------------------------------

// makeCluster builds a single STRICT_DNS cluster pointing at one backend.
func makeCluster(hostname string) *cluster.Cluster {
	return &cluster.Cluster{
		Name:                 clusterName,
		ConnectTimeout:       durationpb.New(1 * time.Second),
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_STRICT_DNS},
		LbPolicy:             cluster.Cluster_ROUND_ROBIN,
		LoadAssignment: &endpoint.ClusterLoadAssignment{
			ClusterName: clusterName,
			Endpoints: []*endpoint.LocalityLbEndpoints{{
				LbEndpoints: []*endpoint.LbEndpoint{{
					HostIdentifier: &endpoint.LbEndpoint_Endpoint{
						Endpoint: &endpoint.Endpoint{
							Address: &core.Address{
								Address: &core.Address_SocketAddress{
									SocketAddress: &core.SocketAddress{
										Address: hostname,
										PortSpecifier: &core.SocketAddress_PortValue{
											PortValue: backendPort,
										},
									},
								},
							},
						},
					},
				}},
			}},
		},
	}
}

// ---------------------------------------------------------------------------
// Control-plane state
// ---------------------------------------------------------------------------

type controlPlane struct {
	mu      sync.Mutex
	version uint64
	target  string
	snap    cachev3.SnapshotCache
}

func newControlPlane() *controlPlane {
	return &controlPlane{
		snap: cachev3.NewSnapshotCache(false, constantHash{}, stdLogger{}),
	}
}

// apply publishes a brand new snapshot (a new version_info) to every
// connected Envoy over the already-open ADS gRPC stream.
func (c *controlPlane) apply(target string) error {
	addr, ok := map[string]string{"v1": "backend-v1", "v2": "backend-v2"}[target]
	if !ok {
		return &badTarget{target: target}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.version++
	version := time.Now().Format("20060102-150405") + "-" + target

	snapshot, err := cachev3.NewSnapshot(version, map[resource.Type][]cachetypes.Resource{
		resource.ClusterType: {makeCluster(addr)},
	})
	if err != nil {
		return err
	}
	if err := c.snap.SetSnapshot(context.Background(), nodeHashKey, snapshot); err != nil {
		return err
	}
	c.target = target
	stdlog.Printf("[snapshot] published version=%s cluster=%s -> %s:%d", version, clusterName, addr, backendPort)
	return nil
}

func (c *controlPlane) current() (string, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.target, c.version
}

type badTarget struct{ target string }

func (e *badTarget) Error() string { return "unknown target " + e.target + " (use v1 or v2)" }

// ---------------------------------------------------------------------------
// HTTP helper API (the "pretend istiod button")
// ---------------------------------------------------------------------------

func newHTTPHandler(cp *controlPlane) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		target, version := cp.current()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"serving_target": target,
			"version":        version,
			"cluster":        clusterName,
		})
	})

	// /switch        -> toggle v1 <-> v2
	// /switch/v1|v2  -> force a target
	mux.HandleFunc("/switch", func(w http.ResponseWriter, r *http.Request) {
		target := "v2"
		if cur, _ := cp.current(); cur == "v1" {
			target = "v2"
		} else if cur == "v2" {
			target = "v1"
		}
		if err := cp.apply(target); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		target, version := cp.current()
		_ = json.NewEncoder(w).Encode(map[string]string{
			"pushed_target": target,
			"note":          "snapshot pushed over ADS gRPC, no Envoy restart",
		})
		_ = version
	})

	mux.HandleFunc("/switch/", func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Path[len("/switch/"):]
		if err := cp.apply(target); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		target, _ = cp.current()
		_ = json.NewEncoder(w).Encode(map[string]string{"pushed_target": target})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		target, version := cp.current()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Envoy gRPC xDS control plane (ADS/CDS)\n\n" +
			" currently serving: " + target + "\n" +
			" snapshot version:  " + time.Now().Format("(live)") + "\n\n" +
			" GET /switch      -> toggle backend v1 <-> v2\n" +
			" GET /switch/v2   -> pin target\n" +
			" GET /status      -> JSON status\n"))
		_ = version
	})

	return mux
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	grpcAddr := flag.String("grpc", ":18000", "address for the ADS gRPC server")
	httpAddr := flag.String("http", ":19000", "address for the HTTP toggle API")
	target := flag.String("target", initialPoint, "initial backend target (v1 or v2)")
	flag.Parse()

	stdlog.SetFlags(stdlog.LstdFlags | stdlog.Lmicroseconds)

	cp := newControlPlane()
	if err := cp.apply(*target); err != nil {
		stdlog.Fatalf("cannot publish initial snapshot: %v", err)
	}

	ads := serverv3.NewServer(context.Background(), cp.snap, nil)

	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		stdlog.Fatalf("cannot listen on %s: %v", *grpcAddr, err)
	}

	grpcServer := grpc.NewServer()
	// ADS is what the Envoy bootstrap points at (ads_config.grpc_services).
	// CDS/EDS are registered too, so plain (non-aggregated) clients also work.
	discovery.RegisterAggregatedDiscoveryServiceServer(grpcServer, ads)
	clustersvc.RegisterClusterDiscoveryServiceServer(grpcServer, ads)
	endpointsvc.RegisterEndpointDiscoveryServiceServer(grpcServer, ads)

	go func() {
		stdlog.Printf("[grpc] ADS control plane listening on %s", *grpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			stdlog.Fatalf("gRPC server failed: %v", err)
		}
	}()

	stdlog.Printf("[http] toggle API listening on %s", *httpAddr)
	if err := http.ListenAndServe(*httpAddr, newHTTPHandler(cp)); err != nil {
		stdlog.Fatalf("HTTP server failed: %v", err)
	}
	_ = os.Stdout
}
