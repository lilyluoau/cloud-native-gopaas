# GoPaaS Platform

A portfolio project for learning Go microservices and Kubernetes controllers.
The [design status](docs/DESIGN_STATUS.md) records differences from the broader
GoForge design, including the use of Linkerd and workload-management services.

**Cloud-Native GoPaaS Platform Development**

Nginx Ingress → Go-micro API Gateway → Go-micro services → protobuf/gRPC → Consul → Kubernetes

> Service mesh is implemented with **Linkerd** (sidecar + automatic mTLS + AuthorizationPolicy).

## Architecture

```text
Client
  ↓
Nginx Ingress
  ↓
api-gateway (HTTP/REST, go-micro registration)
  ├── rate limiting
  ├── Hystrix circuit breaker
  └── Consul service discovery
        ↓ gRPC / protobuf (go-micro transport)
resource-service ── scheduler-service
        ↓
Kubernetes API (Deployments / ScheduledTask CRD → CronJobs → Jobs)
        ↓
Prometheus / Grafana
```

### Components

| Component | Role |
|---|---|
| `services/api-gateway` | HTTP gateway, rate limit, Hystrix client wrapper, Consul discovery |
| `services/resource-service` | Create/get/list/scale/delete Deployments |
| `services/scheduler-service` | Submit ScheduledTask intents; propagate Kubernetes errors |
| `controllers/scheduledtask` | Reconcile one owned CronJob for recurring execution |
| `deploy/` | Docker Compose, kind/K8s manifests, Prometheus, Grafana |

## Quick start (three demos)

Prereqs: Go 1.22+, Docker, kubectl, kind (`~/sdk/bin/kind`).

```bash
cd gopaas-platform

# 1) Local process demo (Consul in Docker)
make build && ./scripts/run-gopaas-demo.sh

# 2) Docker Compose + Prometheus + Grafana
make compose-demo

# 3) kind + Nginx Ingress + Controller + HPA
make kind-demo
```

Local demo verifies gateway, Consul discovery, gRPC create/get/scale, scheduler,
rate-limit 429, circuit open, and `/metrics`.

### Useful endpoints

- Gateway: `http://127.0.0.1:8080/healthz`
- Consul UI: `http://127.0.0.1:8500`
- Metrics: `http://127.0.0.1:9190/metrics`
- Hystrix stream port: `9095`

Example API calls:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/deployments \
  -H 'Content-Type: application/json' \
  -d '{"namespace":"default","name":"demo-web","image":"nginx:1.25","replicas":2}'

curl -X POST http://127.0.0.1:8080/api/v1/deployments/demo-web/scale?namespace=default \
  -H 'Content-Type: application/json' \
  -d '{"replicas":3}'

curl -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"namespace":"default","name":"demo-task","image":"busybox:1.36","schedule":"*/1 * * * *"}'
```

## Docker Compose (Gateway + Consul + Prometheus + Grafana)

```bash
make compose-demo
# Gateway http://127.0.0.1:8080
# Prometheus http://127.0.0.1:9090
# Grafana http://127.0.0.1:13000 (admin/admin)
```

## Kubernetes (kind)

```bash
make kind-up     # create kind + nginx ingress + deploy all
make kind-demo   # verify gateway/ingress, scale, controller→Job, metrics

# manual checks
curl -H 'Host: gopaas.local' http://127.0.0.1:8088/healthz
kubectl -n gopaas get pods,ingress,hpa
kubectl get scheduledtasks,jobs
kubectl -n gopaas port-forward svc/prometheus 9090:9090
kubectl -n gopaas port-forward svc/grafana 3000:3000
```

Workloads run with `GOPAAS_MODE=k8s` (real client-go Deployment/CRD operations).

## Resume mapping / honesty notes

| Resume claim | Evidence in this repo |
|---|---|
| Go-micro v3 microservices | All 3 services register/call via go-micro v3 + Consul |
| gRPC + protobuf | `.proto` + generated stubs; gRPC transport plugin |
| Consul discovery/config | Registry + KV `gopaas/config` |
| API Gateway / rate limit / circuit breaker | Gateway HTTP + limiter + Hystrix wrapper |
| Kubernetes / Ingress / scaling | manifests + HPA + scale demo |
| Custom controller | ScheduledTask CRD + reconciler → Job |
| Prometheus / Grafana | `/metrics`, scrape config, dashboard |
| Service mesh (Linkerd) | Sidecar inject + mTLS certs + AuthorizationPolicy |

Course materials under `../Cloud go paas` remain reference-only and are not required to start this platform.

## Service mesh (Linkerd)

```bash
make mesh-up     # Gateway API CRDs + Linkerd control plane + inject
make mesh-demo   # sidecars 2/2, linkerd check, identity certs, policy
```

Evidence you should see:

- `api-gateway` / `resource-service` / `scheduler-service` pods are **2/2** (app + `linkerd-proxy`)
- `linkerd check --proxy -n gopaas` passes (data-plane proxies ready, certs match CA)
- `linkerd identity -n gopaas -l app=api-gateway` shows workload cert issued by `identity.linkerd`
- `Server` + `AuthorizationPolicy` require meshed mTLS clients for resource-service:8081

## Optional next

- `linkerd viz install` for edges/dashboard
- OpenTelemetry traces
- Benchmark numbers only after real load tests

## Troubleshooting local demos

Run commands from the repository root (`.../CloudNative/gopaas-platform`). If the
prompt already ends in `gopaas-platform %`, do not run `cd gopaas-platform` again.

`make build` uses `$HOME/sdk/go/bin/go` as a fallback, so a system-wide Go
installation is not required. Module downloads still require network access.

The local demo cleans its own PID files and retries service registration. If an
older run was interrupted before this cleanup logic existed, stop only the
documented demo ports and rerun:

```bash
for port in 8080 8081 8083 9091 9093 9095 9190 9191 9193; do
  ids=$(lsof -ti tcp:$port 2>/dev/null || true)
  [[ -z "$ids" ]] || kill $ids 2>/dev/null || true
done
./scripts/run-gopaas-demo.sh
```

Compose waits for the backend services to register in Consul before testing the
Gateway, so a transient startup `502` should no longer fail the demo.
