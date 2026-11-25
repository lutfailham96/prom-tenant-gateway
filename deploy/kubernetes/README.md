# Kubernetes Deployment Manifests

Production-ready Kubernetes manifests for `prom-tenant-gateway`.

## Quick Start

```bash
# Preview rendered manifests
kustomize build deploy/kubernetes

# Apply to cluster
kubectl apply -k deploy/kubernetes

# Check status
kubectl -n mimir get all -l app=prom-tenant-gateway
```

## Required: Create the Token Secret

The token Secret is **not** part of the kustomization, so `kubectl apply -k`
never overwrites real tokens. Create it once (and update it the same way):

```bash
# tokens.env is gitignored; one TENANT_<ID>_TOKEN=<token> per line
cat > tokens.env <<EOT
TENANT_PROD_TOKEN=$(openssl rand -hex 32)
TENANT_STAGING_TOKEN=$(openssl rand -hex 32)
EOT

kubectl -n mimir create secret generic prom-tenant-gateway-tokens \
  --from-env-file=tokens.env --dry-run=client -o yaml | kubectl apply -f -
```

The gateway refuses to start with `CHANGE_ME*` placeholder tokens and logs a
warning for tokens shorter than 32 characters. For GitOps, manage the Secret
with External Secrets or Sealed Secrets instead. After changing tokens, restart
the pods: `kubectl -n mimir rollout restart deployment/prom-tenant-gateway`.

## Components

| File | Description |
|------|-------------|
| `00-namespace.yaml` | `mimir` namespace — **not** in kustomization (owned by Mimir); apply manually only if missing |
| `01-configmap.yaml` | App config (rate limits, log level, etc.) |
| `02-secret.example.yaml` | Token Secret example — **not** in kustomization; create the Secret out of band (see above) |
| `03-deployment.yaml` | Probes, resources, security, preStop drain, `GOMEMLIMIT` (replicas managed by HPA) |
| `04-service.yaml` | ClusterIP service (port 80 → 8080) |
| `05-hpa.yaml` | HPA: CPU 70%, 3-10 replicas |
| `06-poddisruptionbudget.yaml` | Min 2 available during disruptions |
| `07-rbac.yaml` | ServiceAccount only — no Role, no API token mounted |
| `kustomization.yaml` | Kustomize config (images, labels, generators) |

## Security Features

- Non-root user (UID 10001)
- Read-only root filesystem
- Dropped all capabilities
- No privilege escalation
- Pod anti-affinity (spread across nodes)
- No Kubernetes API access (no RBAC, `automountServiceAccountToken: false`)

## Image Updates

Update image tag in `kustomization.yaml`:

```yaml
images:
- name: lutfailham/prom-tenant-gateway
  newTag: "1.2.3"  # or "latest"
```

Or via CLI:

```bash
kustomize edit set image lutfailham/prom-tenant-gateway=1.2.3
kubectl apply -k deploy/kubernetes
```

## Monitoring

ServiceMonitor for Prometheus (add to your monitoring stack):

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: prom-tenant-gateway
  namespace: mimir
spec:
  selector:
    matchLabels:
      app: prom-tenant-gateway
  endpoints:
  - port: http
    path: /metrics
    interval: 30s
```

## Gateway API Integration

For Mimir multi-tenant, use the Gateway API manifests in `deploy/gateway-api/`:
- `01-gateway.yaml` - GatewayClass + Gateway
- `02-middleware-auth.yaml` - Traefik Middleware (auth)
- `03-httproute-internal.yaml` - Internal HTTPRoute
- `04-httproute-https.yaml` - HTTPS HTTPRoute (TLS)

## Verification

```bash
# Health check
kubectl -n mimir port-forward svc/prom-tenant-gateway-svc 8080:80
curl localhost:8080/healthz

# Token validation
curl -H "Authorization: Bearer ***" localhost:8080/validate-token

# Metrics
curl localhost:8080/metrics
```

## Rollback

```bash
kubectl -n mimir rollout undo deployment/prom-tenant-gateway
kubectl -n mimir rollout status deployment/prom-tenant-gateway
```