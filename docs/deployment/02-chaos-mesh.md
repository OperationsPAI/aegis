# 02. Chaos Mesh

Chaos Mesh was installed successfully on the local `kind` cluster `aegis-local`.

## Install source chosen

The repo-owned bootstrap path documented elsewhere in this workspace expands to:

```bash
helm repo add chaos-mesh https://charts.chaos-mesh.org --force-update
helm install chaos-mesh chaos-mesh/chaos-mesh \
  --namespace chaos-mesh \
  --create-namespace \
  --version 2.8.0 \
  -f AegisLab/manifests/cn_mirror/chaos-mesh.yaml \
  --atomic \
  --timeout 10m
```

For this local cluster run, I kept the same chart source and version (`chaos-mesh/chaos-mesh` `2.8.0`) but omitted `-f AegisLab/manifests/cn_mirror/chaos-mesh.yaml`.

Why:
- the submodule contents are not checked out in this docs-only worktree, so the values file was not available locally
- that values file is already documented in this repo as rewriting images to the private CN mirror registry `pair-diag-cn-guangzhou.cr.volces.com/pair/...`
- the local cluster was able to pull the upstream public images from `ghcr.io/chaos-mesh/*`, so the smallest working local adaptation was to install the exact same chart version without the private-registry override

## Cluster precheck

Command:

```bash
kind get clusters
kubectl config current-context
kubectl get nodes
```

Output:

```text
aegis-local
kind-aegis-local
NAME                        STATUS   ROLES           AGE   VERSION
aegis-local-control-plane   Ready    control-plane   17m   v1.34.0
aegis-local-worker          Ready    <none>          17m   v1.34.0
aegis-local-worker2         Ready    <none>          17m   v1.34.0
```

## Helm install

Command:

```bash
helm repo add chaos-mesh https://charts.chaos-mesh.org --force-update
```

Output:

```text
"chaos-mesh" has been added to your repositories
```

Command:

```bash
helm repo update
helm install chaos-mesh chaos-mesh/chaos-mesh \
  --namespace chaos-mesh \
  --create-namespace \
  --version 2.8.0 \
  --atomic \
  --timeout 10m
```

Output:

```text
Hang tight while we grab the latest from your chart repositories...
...Successfully got an update from the "chaos-mesh" chart repository
Update Complete. ⎈Happy Helming!⎈
I0417 14:34:25.475883 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.475920 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.491551 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.491566 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.509591 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.526688 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.544434 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.544453 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.570623 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.570639 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.591727 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.611705 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.611721 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.625396 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.625413 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.639601 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.639617 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.652124 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.742323 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.742352 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.763944 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:25.934236 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:25.934251 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:26.013313 2199859 warnings.go:107] "Warning: unrecognized format \"int32\""
I0417 14:34:26.013332 2199859 warnings.go:107] "Warning: unrecognized format \"int64\""
I0417 14:34:29.232971 2199859 warnings.go:107] "Warning: spec.SessionAffinity is ignored for headless services"
NAME: chaos-mesh
LAST DEPLOYED: Fri Apr 17 14:34:28 2026
NAMESPACE: chaos-mesh
STATUS: deployed
REVISION: 1
TEST SUITE: None
NOTES:
1. Make sure chaos-mesh components are running
   kubectl get pods --namespace chaos-mesh -l app.kubernetes.io/instance=chaos-mesh
```

## Verification after 2 minutes

At `Fri Apr 17 14:36:45 IST 2026`, the Chaos Mesh pods had been up for `2m16s` with no `CrashLoopBackOff` or `ImagePullBackOff`.

Command:

```bash
helm list -n chaos-mesh
```

Output:

```text
NAME      	NAMESPACE 	REVISION	UPDATED                                	STATUS  	CHART           	APP VERSION
chaos-mesh	chaos-mesh	1       	2026-04-17 14:34:28.211479754 +0100 IST	deployed	chaos-mesh-2.8.0	2.8.0
```

Command:

```bash
kubectl get pods -n chaos-mesh -o wide
```

Output:

```text
NAME                                        READY   STATUS    RESTARTS   AGE     IP           NODE                  NOMINATED NODE   READINESS GATES
chaos-controller-manager-6459cdddd5-dq6s4   1/1     Running   0          2m16s   10.244.1.6   aegis-local-worker    <none>           <none>
chaos-controller-manager-6459cdddd5-gtcpw   1/1     Running   0          2m16s   10.244.1.5   aegis-local-worker    <none>           <none>
chaos-controller-manager-6459cdddd5-q2lxb   1/1     Running   0          2m16s   10.244.2.3   aegis-local-worker2   <none>           <none>
chaos-daemon-jpgmb                          1/1     Running   0          2m16s   10.244.1.3   aegis-local-worker    <none>           <none>
chaos-daemon-zlnwn                          1/1     Running   0          2m16s   10.244.2.2   aegis-local-worker2   <none>           <none>
chaos-dashboard-74d68b66c6-82twq            1/1     Running   0          2m16s   10.244.1.2   aegis-local-worker    <none>           <none>
chaos-dns-server-7449ff89-lltdm             1/1     Running   0          2m16s   10.244.1.4   aegis-local-worker    <none>           <none>
```

Command:

```bash
kubectl api-resources | rg 'chaos-mesh|networkchaos'
```

Output:

```text
awschaos                                         chaos-mesh.org/v1alpha1           true         AWSChaos
azurechaos                                       chaos-mesh.org/v1alpha1           true         AzureChaos
blockchaos                                       chaos-mesh.org/v1alpha1           true         BlockChaos
dnschaos                                         chaos-mesh.org/v1alpha1           true         DNSChaos
gcpchaos                                         chaos-mesh.org/v1alpha1           true         GCPChaos
httpchaos                                        chaos-mesh.org/v1alpha1           true         HTTPChaos
iochaos                                          chaos-mesh.org/v1alpha1           true         IOChaos
jvmchaos                                         chaos-mesh.org/v1alpha1           true         JVMChaos
kernelchaos                                      chaos-mesh.org/v1alpha1           true         KernelChaos
networkchaos                                     chaos-mesh.org/v1alpha1           true         NetworkChaos
physicalmachinechaos                             chaos-mesh.org/v1alpha1           true         PhysicalMachineChaos
physicalmachines                                 chaos-mesh.org/v1alpha1           true         PhysicalMachine
podchaos                                         chaos-mesh.org/v1alpha1           true         PodChaos
podhttpchaos                                     chaos-mesh.org/v1alpha1           true         PodHttpChaos
podiochaos                                       chaos-mesh.org/v1alpha1           true         PodIOChaos
podnetworkchaos                                  chaos-mesh.org/v1alpha1           true         PodNetworkChaos
remoteclusters                                   chaos-mesh.org/v1alpha1           false        RemoteCluster
schedules                                        chaos-mesh.org/v1alpha1           true         Schedule
statuschecks                                     chaos-mesh.org/v1alpha1           true         StatusCheck
stresschaos                                      chaos-mesh.org/v1alpha1           true         StressChaos
timechaos                                        chaos-mesh.org/v1alpha1           true         TimeChaos
workflownodes                       wfn          chaos-mesh.org/v1alpha1           true         WorkflowNode
workflows                           wf           chaos-mesh.org/v1alpha1           true         Workflow
```

## CRD smoke test

Manifest applied:
- [networkchaos-smoke.yaml](./networkchaos-smoke.yaml)

Command:

```bash
kubectl create namespace exp --dry-run=client -o yaml | kubectl apply -f -
```

Output:

```text
namespace/exp created
```

Command:

```bash
kubectl apply -f docs/deployment/networkchaos-smoke.yaml
```

Output:

```text
networkchaos.chaos-mesh.org/smoke-delay created
```

Command:

```bash
kubectl get networkchaos -n exp
```

Output:

```text
NAME          ACTION   DURATION
smoke-delay   delay    30s
```

Command:

```bash
kubectl describe networkchaos -n exp smoke-delay | sed -n '1,120p'
```

Output:

```text
Name:         smoke-delay
Namespace:    exp
Labels:       <none>
Annotations:  <none>
API Version:  chaos-mesh.org/v1alpha1
Kind:         NetworkChaos
Metadata:
  Creation Timestamp:  2026-04-17T13:37:11Z
  Finalizers:
    chaos-mesh/records
  Generation:        3
  Resource Version:  2855
  UID:               2397cda4-cd28-419e-b44c-6b89749f3ef3
Spec:
  Action:  delay
  Delay:
    Correlation:  0
    Jitter:       0ms
    Latency:      10ms
  Direction:      to
  Duration:       30s
  Mode:           all
  Selector:
    Namespaces:
      default
Status:
  Conditions:
    Status:  False
    Type:    AllInjected
    Status:  False
    Type:    AllRecovered
    Status:  False
    Type:    Paused
    Status:  False
    Type:    Selected
  Experiment:
    Desired Phase:  Run
Events:
  Type     Reason           Age   From            Message
  ----     ------           ----  ----            -------
  Normal   FinalizerInited  0s    initFinalizers  Finalizer has been inited
  Normal   Updated          0s    initFinalizers  Successfully update finalizer of resource
  Normal   Started          0s    desiredphase    Experiment has started
  Normal   Updated          0s    desiredphase    Successfully update desiredPhase of resource
  Warning  Failed           0s    records         Failed to select targets: no pod is selected
  Warning  Failed           0s    records         Failed to select targets: no pod is selected
```

This was intentionally a CRD smoke test only. The resource was admitted and listed by the API server before cleanup. Because the selector targeted the `default` namespace and there were no matching pods there, the controller recorded `Failed to select targets: no pod is selected`, which does not block CRD registration verification.

Command:

```bash
kubectl delete -f docs/deployment/networkchaos-smoke.yaml
```

Output:

```text
networkchaos.chaos-mesh.org "smoke-delay" deleted from exp namespace
```
