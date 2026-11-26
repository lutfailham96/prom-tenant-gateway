# Gateway API Manifests for prom-tenant-gateway

Routes that expose Mimir through the shared Traefik Gateway, authenticated by
prom-tenant-gateway via a Traefik ForwardAuth middleware.

## Structure

```
deploy/gateway-api/
├── 02-middleware-auth.yaml          # ForwardAuth middleware -> /validate-token
├── 04-httproute-https.yaml          # HTTPS route with auth (push + query)
├── 05-httproute-http-redirect.yaml  # HTTP -> HTTPS redirect (RequestRedirect, no backends)
└── kustomization.yaml
```

The `GatewayClass` and the Gateway itself are **not** part of this
directory: the routes attach to the shared Gateway
`traefik/traefik-internal-gateway`, owned by the Traefik/platform installation.

| Route | Gateway listener (`sectionName`) | Host | Behaviour |
|-------|----------------------------------|------|-----------|
| `prom-tenant-gateway-internal` | `https-metrics-v2-internal-obv` (443) | `metrics-v2.internal.obv.cloudstream.tech` | `/api/v1/push` → distributor, `/prometheus` → query-frontend, both behind ForwardAuth |
| `mimir-httproute-http-redirect` | `http` (80) | `metrics-v2.internal.obv.cloudstream.tech` | `301` to `https://…:443`, no backends |

## Prerequisites

1. **Traefik v3.0+** as Gateway controller with the Traefik CRDs (`traefik.io/v1alpha1` Middleware)
2. **Gateway API CRDs** (`gateway.networking.k8s.io/v1`)
3. Shared Gateway `traefik/traefik-internal-gateway` with listeners `http` and
   `https-metrics-v2-internal-obv`, whose `allowedRoutes` admit routes from the
   `mimir` namespace, and a TLS certificate for the host
4. **prom-tenant-gateway** deployed in `mimir` (`deploy/kubernetes/`); its
   NetworkPolicy must allow the `traefik` namespace
5. Mimir services `mimir-distributed-distributor-headless` and
   `mimir-distributed-query-frontend` in `mimir`

## Deploy

```bash
kubectl kustomize deploy/gateway-api          # preview
kubectl apply -k deploy/gateway-api
```

## Verify

```bash
# Routes accepted by the shared Gateway (Accepted / ResolvedRefs: True)
kubectl -n mimir get httproute prom-tenant-gateway-internal mimir-httproute-http-redirect -o yaml

# Middleware
kubectl -n mimir get middleware prom-tenant-gateway-middleware
```

## Test

```bash
H=metrics-v2.internal.obv.cloudstream.tech

# Valid token
curl -H "Authorization: Bearer <valid-token>" https://$H/prometheus/api/v1/labels

# HTTP redirect (301 to https, never proxied to Mimir)
curl -v http://$H/api/v1/push

# Invalid / missing token -> 401 (429 after too many failures from one client)
curl -H "Authorization: Bearer invalid" https://$H/api/v1/push
curl https://$H/api/v1/push
```

## Notes

- `ExtensionRef` is a `LocalObjectReference` (group, kind, name): the
  Middleware must be in the route's namespace and the reference has no
  `namespace` field. Because the filter and all `backendRefs` are in `mimir`,
  no `ReferenceGrant` is needed.
- The HTTP → HTTPS redirect uses the native `RequestRedirect` filter. The
  redirect route has no `backendRefs`, so plain HTTP can never reach Mimir
  unauthenticated, even if a filter were misconfigured.

## Mapping from Old Ingress

| Ingress Feature | Gateway API Equivalent |
|----------------|------------------------|
| `nginx.ingress.kubernetes.io/auth-url` | `Middleware` (ForwardAuth) + `ExtensionRef` filter |
| `auth-response-headers: X-Scope-OrgID` | `Middleware.forwardAuth.authResponseHeaders` |
| `auth-snippet` (proxy_set_header) | `Middleware.forwardAuth.authRequestHeaders` |
| `force-ssl-redirect` + `ssl-redirect` | HTTP route with `RequestRedirect` filter |
| `cert-manager.io/cluster-issuer` | Certificate on the shared Gateway listener (platform-owned) |
| `ingressClassName: nginx` | `parentRefs` to `traefik/traefik-internal-gateway` |
