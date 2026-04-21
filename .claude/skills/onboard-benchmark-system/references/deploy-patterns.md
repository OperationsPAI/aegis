# Deploy patterns — phase 2 example (Online Boutique)

Two kinds of systems need onboarding:

1. **Already supported by `chaos-experiment`** — has a metadata dir
   like `internal/hs/`, `internal/ts/`. The aegis backend knows the
   service topology. Deploy from whichever upstream the repo points
   to, then jump to phase 5.
2. **Net-new** — no internal metadata. Deploy stand-alone; accept that
   phase 4 will land on CRD-direct injection (path 3), not aegisctl.

## Case 1: supported system (TrainTicket example)

```bash
# Usually there's an upstream deploy script. Read
# chaos-experiment/internal/ts/ for namespace and app-label conventions
# before applying anything.
kubectl apply -n ts -f <upstream-train-ticket-manifest>
kubectl -n ts wait --for=condition=available deploy --all --timeout=600s
```

After `available`, confirm that the actual entry-service works
(`curl http://ts-ui.<ns>:8080/` through a port-forward). `available`
only means pods are Ready, not that the app is serving.

## Case 2: net-new system (Online Boutique — what this session did)

```bash
kubectl create namespace demo
kubectl -n demo apply -f \
  https://raw.githubusercontent.com/GoogleCloudPlatform/microservices-demo/v0.10.2/release/kubernetes-manifests.yaml
kubectl -n demo wait --for=condition=available --timeout=300s deploy --all
```

12 deployments, 12 services, built-in `loadgenerator` — you get
continuous traffic for free, no need to drive load yourself.

Always port-forward on a **non-conflicting host port**. `18080` on this
machine collided with a Flutter IDE process bound to `127.0.0.1` on
IPv4 while `kubectl port-forward` bound `[::1]` on IPv6, so `curl
http://127.0.0.1:18080` silently hit the wrong process. `28080` was
free. Check with `ss -ltnp | grep :<port>` before committing to a port
in your docs.

## Checklist for picking a demo

- [ ] All images pull from a public registry (no internal mirrors).
- [ ] Has a built-in load source, or you're willing to add one.
- [ ] Exposes at least one HTTP/gRPC entry point reachable via
      `kubectl port-forward`.
- [ ] Pods total < ~30 — kind worker nodes get unhappy past that
      with the default kubelet pod cap.
- [ ] Either ships OTEL instrumentation by default, or you know
      exactly which env vars turn it on (see
      `instrumentation-patterns.md`).
