# Gateway API Manifests for go-grafana-auth-service

Native Kubernetes Gateway API manifests using Traefik as the Gateway controller.

## Structure

```
deploy/gateway-api/
├── 00-gatewayclass.yaml          # Cluster-scoped (apply once per cluster)
├── 01-gateway.yaml               # Gateway with HTTP + HTTPS listeners
├── 02-middleware-auth.yaml       # ForwardAuth middleware (auth validation)
├── 03-middleware-redirect.yaml   # HTTP -> HTTPS redirect middleware
├── 04-httproute-https.yaml       # HTTPS route with auth
├── 05-httproute-http-redirect.yaml # HTTP route (redirect only)
├── 06-referencegrant.yaml        # Allows HTTPRoute to reference Middleware
└── kustomization.yaml            # Kustomize deployment
```

## Prerequisites

1. **Traefik v3.0+** installed as Gateway controller
2. **Gateway API CRDs** installed (`gateway.networking.k8s.io/*`)
3. **cert-manager** with `letsencrypt-dns-prod` ClusterIssuer
4. **go-grafana-auth-service** deployed in `mimir` namespace
5. **mimir-distributed-distributor-headless** service in `mimir` namespace

## Deploy

### Option 1: Kustomize (recommended)
```bash
# 1. Apply GatewayClass (cluster-scoped, needs admin)
kubectl apply -f 00-gatewayclass.yaml

# 2. Apply namespace-scoped resources
kubectl apply -k deploy/gateway-api/
```

### Option 2: Individual files (for specific ordering)
```bash
# 1. GatewayClass (cluster-scoped)
kubectl apply -f 00-gatewayclass.yaml

# 2. Gateway + Middleware + HTTPRoute + ReferenceGrant
kubectl apply -f 01-gateway.yaml
kubectl apply -f 02-middleware-auth.yaml
kubectl apply -f 03-middleware-redirect.yaml
kubectl apply -f 04-httproute-https.yaml
kubectl apply -f 05-httproute-http-redirect.yaml
kubectl apply -f 06-referencegrant.yaml
```

## Verify

```bash
# Check GatewayClass
kubectl get gatewayclass traefik -o yaml

# Check Gateway status (should show Ready: True)
kubectl get gateway -n mimir traefik-gateway -o yaml

# Check HTTPRoute status (should show Accepted: True)
kubectl get httproute -n mimir -o yaml

# Check Middleware
kubectl get middleware -n mimir

# Check ReferenceGrant
kubectl get referencegrant -n mimir
```

## Test

```bash
# Test HTTPS endpoint with valid token
curl -H "Authorization: Bearer <valid-token>" https://metrics.obv.cloudstream.tech/api/v1/push

# Test HTTP redirect
curl -v http://metrics.obv.cloudstream.tech/api/v1/push  # Should 301 to HTTPS

# Test invalid token
curl -H "Authorization: Bearer invalid" https://metrics.obv.cloudstream.tech/api/v1/push  # Should 401

# Test missing auth
curl https://metrics.obv.cloudstream.tech/api/v1/push  # Should 401
```

## Mapping from Old Ingress

| Ingress Feature | Gateway API Equivalent |
|----------------|------------------------|
| `nginx.ingress.kubernetes.io/auth-url` | `Middleware` (ForwardAuth) + `ExtensionRef` filter |
| `auth-response-headers: X-Scope-OrgID` | `Middleware.forwardAuth.authResponseHeaders` |
| `auth-snippet` (proxy_set_header) | `Middleware.forwardAuth.authRequestHeaders` |
| `force-ssl-redirect` + `ssl-redirect` | HTTP listener + `RedirectScheme` Middleware + separate HTTPRoute |
| `cert-manager.io/cluster-issuer` | Annotation on `Gateway` |
| `ingressClassName: nginx` | `GatewayClass.controllerName: traefik.io/gateway-controller` |

## Notes

- **GatewayClass** is cluster-scoped — apply once per cluster (requires admin)
- **ReferenceGrant** allows HTTPRoute to reference Middleware in same namespace
- **ExtensionRef** filter requires Traefik Middleware CRD (`traefik.io/v1alpha1`)
- TLS certificate managed by cert-manager via Gateway annotation
- HTTP listener redirects to HTTPS via Middleware (not built-in Gateway API feature yet)