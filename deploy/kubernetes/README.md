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
| `08-networkpolicy.yaml` | Ingress only from the ingress-nginx/Traefik and monitoring namespaces (adjust names per cluster) |
| `kustomization.yaml` | Kustomize config (images, labels, generators) |

## Security Features

- Non-root user (UID 10001)
- Read-only root filesystem
- Dropped all capabilities
- No privilege escalation
- Pod anti-affinity (spread across nodes)
- No Kubernetes API access (no RBAC, `automountServiceAccountToken: false`)
- NetworkPolicy: only the ingress controller and the monitoring stack can
  reach the gateway (`08-networkpolicy.yaml`)
- `TRUSTED_PROXIES` defaults to loopback only. Set it to the ingress pod IPs/CIDR
  of the target cluster for per-client rate limiting — never to whole RFC1918
  ranges (see the comment in `01-configmap.yaml`)

## Image Updates

The image tag lives in one place: `image:` in `03-deployment.yaml` (pinned,
never `:latest`). Releases are built by CI from a git tag: pushing `v1.2.3`
publishes `lutfailham/prom-tenant-gateway:1.2.3` with version `1.2.3` baked into
the binary (`-ldflags -X main.version`), so `/healthz` and
`prom_tenant_gateway_build_info` always match the running image.

```bash
git tag v1.2.3 && git push origin v1.2.3          # CI builds and pushes 1.2.3
sed -i 's#prom-tenant-gateway:.*#prom-tenant-gateway:1.2.3#' deploy/kubernetes/03-deployment.yaml
kubectl apply -k deploy/kubernetes
kubectl -n mimir exec deploy/prom-tenant-gateway -- wget -qO- http://127.0.0.1:8080/healthz  # "version":"1.2.3"
```

For stronger immutability pin the digest printed by CI instead
(`image: lutfailham/prom-tenant-gateway:1.2.3@sha256:...`).

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

For Mimir multi-tenant, use the Gateway API manifests in `deploy/gateway-api/`
(routes on the shared Gateway `traefik/traefik-internal-gateway`):
- `02-middleware-auth.yaml` - Traefik ForwardAuth Middleware
- `04-httproute-https.yaml` - HTTPS HTTPRoute (push + query, with auth)
- `05-httproute-http-redirect.yaml` - HTTP → HTTPS redirect, no backends

`08-networkpolicy.yaml` must allow the `traefik` namespace for ForwardAuth.

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