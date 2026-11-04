#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export PATH="${HOME}/sdk/bin:${HOME}/sdk/go/bin:${HOME}/go/bin:/opt/homebrew/bin:/usr/local/bin:${PATH}"

CLUSTER_NAME="${CLUSTER_NAME:-gopaas}"
CONTEXT="kind-${CLUSTER_NAME}"
GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8088}"
HOST_HEADER="${HOST_HEADER:-gopaas.local}"

PASS=0
FAIL=0
ok() { echo "✔ $1"; PASS=$((PASS+1)); }
bad() { echo "✘ $1"; FAIL=$((FAIL+1)); }

curl_gw() {
  curl -sS -H "Host: ${HOST_HEADER}" "$@"
}

echo "== GoPaaS kind/k8s demo =="

# ensure cluster deployed
if ! kubectl --context "$CONTEXT" -n gopaas get deploy/api-gateway >/dev/null 2>&1; then
  bash "${ROOT}/scripts/deploy-k8s.sh"
fi

# port-forward fallback if ingress not ready
use_pf=0
if ! curl -sS -o /dev/null -w '%{http_code}' -H "Host: ${HOST_HEADER}" "${GATEWAY_URL}/healthz" | grep -q 200; then
  echo "ingress not ready yet, using kubectl port-forward..."
  kubectl --context "$CONTEXT" -n gopaas port-forward svc/api-gateway 18080:80 >/tmp/gopaas-pf.log 2>&1 &
  echo $! > /tmp/gopaas-pf.pid
  use_pf=1
  GATEWAY_URL="http://127.0.0.1:18080"
  for i in $(seq 1 30); do
    if curl -fsS "${GATEWAY_URL}/healthz" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
fi
cleanup() {
  if [[ -f /tmp/gopaas-pf.pid ]]; then
    kill "$(cat /tmp/gopaas-pf.pid)" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if curl -fsS -H "Host: ${HOST_HEADER}" "${GATEWAY_URL}/healthz" >/dev/null 2>&1 || curl -fsS "${GATEWAY_URL}/healthz" >/dev/null 2>&1; then
  ok "Gateway reachable via ${GATEWAY_URL}"
else
  bad "Gateway not reachable"
fi

# create deployment through gateway (k8s mode)
NAME="k8s-demo-$(date +%s | tail -c 5)"
# pause is already present on kind nodes — avoids Docker Hub pull flakiness
body=$(printf '{"namespace":"default","name":"%s","image":"registry.k8s.io/pause:3.10","replicas":1,"container_port":80}' "$NAME")
if curl_gw -fsS -X POST "${GATEWAY_URL}/api/v1/deployments" -H 'Content-Type: application/json' -d "$body" >/tmp/gopaas-create.json; then
  ok "created Deployment via Gateway/gRPC"
else
  bad "create deployment failed"; cat /tmp/gopaas-create.json || true
fi

# wait and verify in kubernetes
for i in $(seq 1 40); do
  if kubectl --context "$CONTEXT" -n default get deploy "$NAME" >/dev/null 2>&1; then
    ok "kubectl sees Deployment/${NAME}"
    break
  fi
  sleep 1
  if [[ $i -eq 40 ]]; then bad "Deployment not found in cluster"; fi
done

# scale
if curl_gw -fsS -X POST "${GATEWAY_URL}/api/v1/deployments/${NAME}/scale?namespace=default" \
  -H 'Content-Type: application/json' -d '{"replicas":3}' >/tmp/gopaas-scale.json; then
  ok "scaled Deployment via API"
else
  bad "scale API failed"
fi
kubectl --context "$CONTEXT" -n default get deploy "$NAME" -o jsonpath='{.spec.replicas}' | grep -q '^3$' \
  && ok "kubectl confirms replicas=3" || bad "replicas not 3"

# HPA / gateway scale evidence
kubectl --context "$CONTEXT" -n gopaas scale deploy/api-gateway --replicas=2 >/dev/null
kubectl --context "$CONTEXT" -n gopaas rollout status deploy/api-gateway --timeout=120s >/dev/null
replicas=$(kubectl --context "$CONTEXT" -n gopaas get deploy api-gateway -o jsonpath='{.status.readyReplicas}')
[[ "${replicas:-0}" -ge 2 ]] && ok "kubectl scale api-gateway readyReplicas=${replicas}" || bad "gateway scale failed"
kubectl --context "$CONTEXT" -n gopaas get hpa api-gateway >/dev/null && ok "HPA object present" || bad "HPA missing"
# scale back
kubectl --context "$CONTEXT" -n gopaas scale deploy/api-gateway --replicas=1 >/dev/null

# ScheduledTask controller evidence
TASK="demo-task-$(date +%s | tail -c 5)"
cat <<EOF | kubectl --context "$CONTEXT" apply -f -
apiVersion: gopaas.io/v1
kind: ScheduledTask
metadata:
  name: ${TASK}
  namespace: default
spec:
  image: busybox:1.36
  schedule: "*/1 * * * *"
  command: "echo hello-from-controller && date"
EOF
ok "applied ScheduledTask/${TASK}"

job_found=0
for i in $(seq 1 60); do
  if kubectl --context "$CONTEXT" -n default get cronjob "${TASK}" >/dev/null 2>&1; then
    job_found=1
    break
  fi
  # also accept any job labeled with the task
  if kubectl --context "$CONTEXT" -n default get jobs -l "gopaas.io/scheduledtask=${TASK}" --no-headers 2>/dev/null | grep -q .; then
    job_found=1
    break
  fi
  sleep 2
done
if [[ "$job_found" == "1" ]]; then
  ok "controller created CronJob for ScheduledTask"
else
  bad "controller did not create CronJob"
  kubectl --context "$CONTEXT" -n gopaas logs deploy/scheduledtask-controller --tail=40 || true
fi

phase=$(kubectl --context "$CONTEXT" -n default get scheduledtask "$TASK" -o jsonpath='{.status.phase}' 2>/dev/null || true)
[[ -n "$phase" ]] && ok "ScheduledTask status.phase=${phase}" || bad "ScheduledTask status empty"

# metrics
if kubectl --context "$CONTEXT" -n gopaas get pods -l app=api-gateway -o name | head -1 | xargs -I{} kubectl --context "$CONTEXT" -n gopaas exec {} -- wget -qO- http://127.0.0.1:9190/metrics 2>/dev/null | grep -q gopaas_http_requests_total; then
  ok "Prometheus metrics scraped from gateway pod"
else
  # fallback via port-forward metrics
  kubectl --context "$CONTEXT" -n gopaas port-forward svc/api-gateway 19190:9190 >/tmp/gopaas-metrics-pf.log 2>&1 &
  echo $! > /tmp/gopaas-metrics-pf.pid
  sleep 2
  if curl -fsS http://127.0.0.1:19190/metrics | grep -q gopaas_http_requests_total; then
    ok "Prometheus metrics via port-forward"
  else
    bad "metrics not available"
  fi
  kill "$(cat /tmp/gopaas-metrics-pf.pid)" >/dev/null 2>&1 || true
fi

# consul discovery
consul_pod=$(kubectl --context "$CONTEXT" -n gopaas get pod -l app=consul -o jsonpath='{.items[0].metadata.name}')
services=$(kubectl --context "$CONTEXT" -n gopaas exec "$consul_pod" -- wget -qO- http://127.0.0.1:8500/v1/catalog/services 2>/dev/null || true)
echo "$services" | grep -q 'go.micro.service.resource' && ok "Consul discovered resource-service" || bad "Consul missing resource-service"
echo "$services" | grep -q 'go.micro.api.gateway' && ok "Consul discovered api-gateway" || bad "Consul missing api-gateway"

echo
echo "Passed: $PASS  Failed: $FAIL"
kubectl --context "$CONTEXT" -n default get deploy "$NAME" scheduledtask "$TASK" jobs 2>/dev/null || true
kubectl --context "$CONTEXT" -n gopaas get pods
if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
echo
echo "K8s evidence ready:"
echo "  Ingress/Gateway: ${GATEWAY_URL} (Host: ${HOST_HEADER})"
echo "  kind port map:   http://127.0.0.1:8088"
echo "  Prometheus:      kubectl -n gopaas port-forward svc/prometheus 9090:9090"
echo "  Grafana:         kubectl -n gopaas port-forward svc/grafana 3000:3000"
