# Deployment (Kubernetes)

A grpcmux service is one container with three ports: HTTP `8080` (REST, Connect, health), gRPC `8081`, metrics `9090`. Manifests to start from are in [examples/deploy/k8s](../../examples/deploy/k8s) and an Envoy configuration in [examples/deploy/envoy](../../examples/deploy/envoy).

## Probes

```yaml
ports:
  - {name: http, containerPort: 8080}
  - {name: grpc, containerPort: 8081}
  - {name: metrics, containerPort: 9090}
startupProbe:
  httpGet: {path: /healthz, port: http}
  periodSeconds: 2
  failureThreshold: 30
livenessProbe:
  httpGet: {path: /healthz, port: http}
  periodSeconds: 10
readinessProbe:
  grpc: {port: 8081}           # or: httpGet: {path: /readyz, port: http}
  periodSeconds: 2
  failureThreshold: 1
```

- **Liveness** on `/healthz`: always 200 while the process serves HTTP, so a pod is restarted only when it is really stuck, never because a dependency is down or because it is draining.
- **Readiness** on gRPC health or `/readyz`: NOT_SERVING before the gRPC listener is up, from the first moment of shutdown, and whenever a `gs.AddHealthChecker` check fails.

## Shutdown without dropped requests

On SIGTERM the server turns unready, waits `PRE_STOP`, then drains (details in [server.md](server.md#graceful-shutdown)). The wait gives the endpoints controller and load balancers time to stop sending new requests to the pod; keep it at least as long as readiness `periodSeconds × failureThreshold` plus your load balancer's propagation delay.

```yaml
spec:
  terminationGracePeriodSeconds: 60   # > PRE_STOP + longest request + closers
  containers:
    - name: app
      env:
        - {name: PRE_STOP, value: "10s"}
        - {name: GRACEFUL_TIMEOUT, value: "50s"}   # optional; shutdown deadline, below the grace period
```

`terminationGracePeriodSeconds` must exceed `PRE_STOP` plus the time in-flight requests and streams need to finish; otherwise the kubelet sends SIGKILL in the middle of the drain. Long-lived streams (server streaming, WebSockets) end when `GRACEFUL_TIMEOUT` (default `6m`) or the grace period runs out, so clients should reconnect.

Add a PodDisruptionBudget and at least two replicas: PRE_STOP protects against traffic still arriving at a terminating pod, the PDB against all pods terminating at once.

## Load balancing gRPC

gRPC multiplexes requests over long-lived HTTP/2 connections, so a plain Kubernetes Service (L4) pins each client to one pod. grpcmux servers recycle connections every minute (`MaxConnectionAge`) to rebalance; for even spreading use an L7 proxy (Envoy Gateway, a service mesh) or a headless Service with client-side `round_robin` (the grpcmux client default with `dns://`, see [grpc-client.md](grpc-client.md)).

## Behind Envoy Gateway / Gateway API

TLS terminates at the gateway; the backend speaks h2c on port 8080. Declare the backend protocol so Envoy uses HTTP/2 upstream (required for gRPC and Connect streaming through the HTTP port):

```yaml
apiVersion: v1
kind: Service
spec:
  ports:
    - name: http
      port: 8080
      appProtocol: kubernetes.io/h2c
```

Route `/pkg.Service/*` (Connect) and your REST prefixes with an `HTTPRoute` (or a `GRPCRoute` to port 8081 for native gRPC). Don't set request timeouts shorter than your longest stream on routes that carry streams. If the gateway handles CORS, set `CORSConfig{Disabled: true}`.

## Observability

- Metrics: scrape `:9090/metrics` (gRPC server/client metrics with exemplars, HTTP client metrics, panics, Go runtime).
- Tracing: enable `tracing: true` and install an OpenTelemetry TracerProvider (OTLP exporter) in `main`.
- Logs: JSON on stdout ([logging.md](logging.md)).
