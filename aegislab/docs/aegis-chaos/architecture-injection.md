# Fault-Injection Architecture (post-refactor, 2026-05-21)

State after PRs #441–#446. `aegislab/src/internal/chaosengine/` no longer
exists; all components live under the canonical four-bucket layout
(`core / crud / platform / cli`).

## Top-level flow

```mermaid
flowchart TD
    subgraph clients["Clients"]
        cli["aegisctl<br/>(cli/cmd/inject_*)"]
        ui["aegis-web UI"]
        ci["CI / kubectl smoke<br/>(static bearer)"]
    end

    subgraph entry["HTTP entry — crud/chaos/routes.go (/v1beta)"]
        auth["middleware.RequireServiceAccount<br/>+ static-bearer fallback"]
        h_pts["handler.go<br/>points / injections / capabilities / manifest"]
        h_g["handler_guided.go<br/>guided/preview · guided/next · guided/enumerate"]
        h_sse["handler_sse.go<br/>status stream"]
    end

    subgraph core["Core — crud/chaos/"]
        svc["service.go ▸ Manager<br/>(UpsertSystem, CreateInjection, ...)"]
        idem["idempotency.go"]
        cap["capability_map.go<br/>ChaosType ▸ capability"]
        rend_reg["renderer.go<br/>Renderer registry"]
        rend_imp["renderer_{podkill,network,http,jvm,<br/>dns,stress,time,podchaos_extra}.go"]
        exec_if["executor.go (Executor iface)"]
        exec_cm["executor_chaosmesh.go<br/>unstructured + dynamic client"]
        recon["reconciler.go<br/>poll → state transition"]
        wh["webhook.go (WebhookSender)"]
        db[(MySQL<br/>injections / batches / points / services)]
    end

    subgraph guided["Guided wizard — crud/chaos/guided/"]
        g_res["resolver.go ▸ Resolve / next decision"]
        g_enum["enumerate.go ▸ EnumerateAllCandidates"]
        g_apply["next.go ▸ ApplyNextSelection"]
        g_build["builders.go ▸ payload per capability"]
        g_view["preview.go"]
        g_k8s["k8s.go · systems.go<br/>live pod/container reads"]
        g_cfg["config.go · types.go<br/>GuidedConfig schema"]
    end

    subgraph platform["Platform — platform/"]
        ccli["k8s/chaosclient<br/>singleton rest.Config + client"]
        rlu["k8s/resourcelookup<br/>app-label · endpoint · container cache"]
        sysc["systemconfig<br/>registry: ts/otel-demo/hs/sn/mm/tt/sock"]
        cmeta["crud/chaos/chaosmeta<br/>chaos-mesh CRD discovery"]
    end

    subgraph k8s["Target cluster"]
        cm["Chaos Mesh CRs<br/>PodChaos · NetworkChaos · StressChaos<br/>TimeChaos · DNSChaos · HTTPChaos"]
        pods[("Target pods<br/>(victim workloads)")]
    end

    cli --> auth
    ui --> auth
    ci --> auth
    auth --> h_pts
    auth --> h_g
    auth --> h_sse

    h_pts --> svc
    h_g  --> g_res
    h_g  --> g_apply
    h_g  --> g_enum
    h_sse --> svc

    svc --> idem
    svc --> cap
    svc --> rend_reg
    rend_reg --> rend_imp
    svc --> exec_if --> exec_cm
    svc --> db

    g_res --> g_build
    g_enum --> g_build
    g_build --> rlu
    g_build --> sysc
    g_k8s --> ccli
    g_res --> g_k8s
    g_enum --> g_k8s
    g_view --> rlu

    rlu --> ccli
    rlu --> sysc
    cmeta --> ccli
    svc --> cmeta

    exec_cm --> ccli
    exec_cm -- "apply / get / delete" --> cm
    cm --> pods

    recon --> db
    recon --> exec_if
    recon --> wh
    wh -. POST .-> ui
```

## Two write paths into `Executor.Apply`

```mermaid
sequenceDiagram
    autonumber
    participant Client
    participant H as crud/chaos/handler*.go
    participant G as crud/chaos/guided
    participant M as Manager (service.go)
    participant R as Renderer (capability-specific)
    participant E as Executor (chaos-mesh impl)
    participant K as Kubernetes API
    participant DB as MySQL

    rect rgb(238,246,255)
    Note over Client,H: Path 1 — direct point/injection (aegisctl, CI)
    Client->>H: POST /v1beta/injections {point_id, params}
    H->>M: CreateInjection
    M->>DB: lookup point + capability
    M->>R: Render(SystemContext, params) → unstructured.Object
    M->>E: Apply(ctx, obj)
    E->>K: dynamic.Create CR
    E-->>M: handle
    M->>DB: persist row(handle, running)
    M-->>Client: 200 {injection_id, handle}
    end

    rect rgb(245,255,240)
    Note over Client,G: Path 2 — interactive guided wizard (UI / aegisctl inject_guided)
    Client->>H: POST /v1beta/chaos/guided/next {partial cfg, value}
    H->>G: ApplyNextSelection
    G->>G: resolver decides next field
    G-->>H: GuidedResponse {next_field, options}
    Note right of G: client iterates until cfg complete
    Client->>H: POST /v1beta/chaos/guided/preview {final cfg}
    H->>G: Resolve → builders → payload
    G-->>H: capability + params
    H->>M: CreateInjection (same path as Path 1)
    M->>R: Render
    M->>E: Apply
    end
```

## Background reconcile loop

```mermaid
sequenceDiagram
    autonumber
    participant T as Ticker (reconciler.go)
    participant DB
    participant E as Executor
    participant W as WebhookSender

    loop every 5s (reconcilerBatchSize=200)
        T->>DB: SELECT pending|running rows
        loop per row
            T->>E: Status(handle)
            E-->>T: Pending|Running|Succeeded|Failed|Orphaned
            alt state changed
                T->>DB: UPDATE row.state, ended_at
                T->>W: enqueue webhook
            end
        end
    end
```

## Component map (post-refactor)

| Layer | Package | Responsibility |
|---|---|---|
| HTTP | `crud/chaos/handler.go` | Singleton & batch injection routes |
| HTTP | `crud/chaos/handler_guided.go` | Guided wizard routes (DTO round-trip into `crud/chaos/guided`) |
| HTTP | `crud/chaos/handler_sse.go` | Status streaming |
| Service | `crud/chaos/service.go` (`Manager`) | Orchestrates DB + renderer + executor |
| Service | `crud/chaos/reconciler.go` | Background CR status polling |
| Service | `crud/chaos/webhook.go` | Outbound state notifications |
| Capability dispatch | `crud/chaos/capability_map.go` | `ChaosType` ⇄ capability name |
| Render | `crud/chaos/renderer.go` + `renderer_*.go` | Capability → unstructured Chaos-Mesh CR |
| Execute | `crud/chaos/executor.go` (iface), `executor_chaosmesh.go` | Apply / Status / Destroy via dynamic client |
| Guided UX | `crud/chaos/guided/` | Decision tree + payload builders + live-cluster reads |
| Metadata | `crud/chaos/chaosmeta/` | Discover installed Chaos-Mesh CRDs |
| Platform | `platform/k8s/chaosclient` | Singleton `*rest.Config` + clients (boot-time init) |
| Platform | `platform/k8s/resourcelookup` | App-label / HTTP-endpoint / container caches |
| Platform | `platform/systemconfig` | System registry (`ts`, `otel-demo`, `hs`, `sn`, `mm`, `tt`, `sock`) + current `AppLabelKey` |

## Boundary contracts

- **`crud/chaos` → Chaos-Mesh CRDs** is the only k8s write path. Every CR
  is produced by exactly one `Renderer`; `executor_chaosmesh.go` applies
  it through one dynamic client. No other package writes Chaos-Mesh CRs.
- **`crud/chaos/guided` does NOT call `Executor`.** It only resolves
  configs into `(capability, params)` and hands back to the handler,
  which then walks the same Path 1 above. This is why both paths share
  Render + Apply.
- **All kube reads go through `platform/k8s/chaosclient`'s cached
  config** since PR #446. `guided/k8s.go` and `guided/systems.go` both
  call `chaosclient.GetK8sConfig()`; there is no second kubeconfig
  discovery anywhere in the chaos stack.
- **`platform/systemconfig` is the single source of truth for system
  identity.** `SystemType` strings (`ts`, `hs`, …) are minted here;
  `renderer_*.go` resolves `AppLabelKey` from it via `SystemContext`.

## Where chaos points come from

Capabilities are emitted by `tools/capgen/` into
`tools/capgen/output/capabilities.json`. `capability_map.go` is the
hand-maintained Go projection of the same identity. Adding a new
capability requires (a) a capgen entry, (b) a `Renderer` registration,
and (c) the capability_map row — the executor/service plumbing is
generic.
