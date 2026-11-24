# Kubernetes Deployment Manifests

Production-ready Kubernetes manifests for `prom-tenant-gateway`.

## Quick Start

```bash
# Preview rendered manifests
kustomize build deploy/kubernetes

# Apply to cluster
kubectl apply -k deploy/kubernetes

# Check status
kubectl -n monitoring get all -l app=prom-tenant-gateway
```

## Required: Update Secrets

**Before applying, replace placeholder tokens in `02-secret.yaml`:**

```yaml
stringData:
  TENANT_PROD_TOKEN: "your-actual-prod-token"
  TENANT_STAGING_TOKEN: "your-actual-staging-token"
```

Or use Kustomize secretGenerator (recommended for CI/CD):

```bash
# Generate secret with actual values
kustomize build deploy/kubernetes --enable-alpha-plugins \
  | kubectl apply -f -
```

## Components

| File | Description |
|------|-------------|
| `00-namespace.yaml` | `monitoring` namespace |
| `01-configmap.yaml` | App config (rate limits, log level, etc.) |
| `02-secret.yaml` | **Tokens** - MUST UPDATE BEFORE APPLY |
| `03-deployment.yaml` | 3 replicas, probes, resources, security |
| `04-service.yaml` | ClusterIP service (port 80 → 8080) |
| `05-hpa.yaml` | HPA: CPU 70%, Memory 80%, 3-10 replicas |
| `06-poddisruptionbudget.yaml` | Min 2 available during disruptions |
| `07-rbac.yaml` | ServiceAccount + minimal RBAC |
| `kustomization.yaml` | Kustomize config (images, labels, generators) |

## Security Features

- Non-root user (UID 10001)
- Read-only root filesystem
- Dropped all capabilities
- No privilege escalation
- Pod anti-affinity (spread across nodes)
- Minimal RBAC

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
  namespace: monitoring
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
kubectl -n monitoring port-forward svc/prom-tenant-gateway-svc 8080:80
curl localhost:8080/healthz

# Token validation
curl -H "Authorization: Bearer <token>" localhost:8080/validate-token

# Metrics
curl localhost:8080/metrics
```

## Rollback

```bash
kubectl -n monitoring rollout undo deployment/prom-tenant-gateway
kubectl -n monitoring rollout status deployment/prom-tenant-gateway
```