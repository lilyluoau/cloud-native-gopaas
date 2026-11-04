# Design status

GoPaaS is a portfolio project for learning Go services and Kubernetes control
loops. The supplied GoForge design describes a broader AI-serving platform;
the repository currently manages Deployments and scheduled workloads.

| Design area | Current implementation and limits |
| --- | --- |
| Gateway and RPC | HTTP gateway, Go-micro v3, protobuf/gRPC, Consul discovery. Not the document's four Model/Auth/Metrics/Gateway services. |
| Resilience | Existing gateway limiter and Hystrix wrapper. No measured 35% reduction in peak failures. |
| Scheduling | ScheduledTask now reconciles one owned CronJob, updates schedule/image/command, forbids overlapping runs, and exposes actual scheduling status. A local cluster test observed two distinct completed Jobs across repeated reconciliation. |
| Failure visibility | Kubernetes errors propagate; a failed CRD request no longer silently becomes an unrelated one-shot Job. |
| Service mesh | Existing Linkerd setup and policy manifests. Istio VirtualServices, DestinationRules and canary promotion from the GoForge document remain unimplemented. |
| Scaling and observability | HPA, Prometheus and Grafana configuration exist. No new load-triggered HPA or sustained SLO measurement was performed in this change. |
| Security | Mesh authentication does not replace end-user authorization. Full JWT/Auth Service flow and production secret handling need separate work. |
| Release/AI serving | Automated canary analysis, model inference services, multi-region recovery and distributed tracing remain future work. |

## Verification

```sh
go test -race ./...
# Opt-in only on a local cluster with the ScheduledTask CRD installed:
GOPAAS_TEST_CONTEXT=kind-gopaas go test ./controllers/scheduledtask/controllers -run TestCronJobCluster -v -timeout=200s
```

On this macOS host, Go 1.22 needs `-ldflags=-linkmode=external` for test binaries.
The local cluster check directly invokes the new reconciler against the API;
it does not claim the running controller deployment was upgraded. Two scheduled
Jobs completed in 85 seconds. Unit tests also cover repeated reconciliation,
spec updates, ownership conflicts, missing objects and propagated permission errors.

CronJob execution can be missed or duplicated; task code must tolerate retries.
There is no exactly-once guarantee or verified 99.95% production uptime.
