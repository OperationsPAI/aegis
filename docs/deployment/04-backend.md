# 04. AegisLab Backend

This pass used the already-running kind cluster `aegis-local` plus the Chaos Mesh install from issue `#7`.

## Result

- Final backend image tag: `aegislab-backend:local`
- Running backend pod label selector: `kubectl get pods -n default -l app=aegislab-backend`
- Working local probe: `curl http://127.0.0.1:14319/health`
- Response body:

```json
{"errors_total":0,"published_total":0,"received_total":0,"status":"healthy"}
```

## Local Build

Build from the parent workspace root so the Docker context includes `AegisLab/src`:

```bash
docker build -f AegisLab/src/Dockerfile \
  -t aegislab-backend:local \
  --build-arg GO_BUILD_TAGS=duckdb_arrow \
  AegisLab
```

Observed build result:

```text
#19 naming to docker.io/library/aegislab-backend:local done
#19 unpacking to docker.io/library/aegislab-backend:local done
```

`duckdb_arrow` was required in the build command because [AegisLab/src/Dockerfile](/home/ddq/AoyangSpace/aegis/AegisLab/src/Dockerfile) forwards `GO_BUILD_TAGS` into `go build`.

## Working Deploy Sequence

### 1. Load the locally built backend image into kind

```bash
kind load docker-image --name aegis-local aegislab-backend:local
```

### 2. Render the repo-owned chart into a local baseline

The checked-in chart is the only repo-owned source that creates the backend StatefulSets, Services, PVCs, ConfigMaps, and RBAC objects. For this pass, the baseline resources were rendered from that chart with local-only values overrides, then patched before the first apply.

Render the baseline manifest:

```bash
mkdir -p /tmp/aegislab-backend-overlay

helm template aegislab-backend ./AegisLab/helm \
  --namespace default \
  --set harbor.enabled=false \
  --set buildkit.enabled=false \
  --set alloy.enabled=false \
  --set loki.enabled=false \
  --set prometheus.enabled=false \
  --set grafana.enabled=false \
  --set rcabench-frontend.enabled=false \
  --set initialization.enabled=false \
  --set configmap.k8s.namespace=default \
  --set persistence.storageType=external \
  --set persistence.storageClassNames.external=local-path \
  --set persistence.redis.storageClass=local-path \
  --set persistence.mysql.storageClass=local-path \
  --set persistence.etcd.storageClass=local-path \
  --set persistence.jaeger.storageClass=local-path \
  --set images.rcabench.name=aegislab-backend \
  --set images.rcabench.tag=local \
  --set images.rcabench.pullPolicy=IfNotPresent \
  --set images.busybox.name=busybox \
  --set images.busybox.tag=1.35 \
  --set images.etcd.name=quay.io/coreos/etcd \
  --set images.etcd.tag=v3.6.7 \
  --set images.redis.name=redis \
  --set images.redis.tag=8.0-M02-alpine3.20 \
  --set images.mysql.name=mysql \
  --set images.mysql.tag=8.0.43 \
  --set images.jaeger.name=jaegertracing/all-in-one \
  --set images.jaeger.tag=latest \
  --set-file initialDataFiles.data_yaml=AegisLab/data/initial_data/prod/data.yaml \
  --set-file initialDataFiles.otel_demo_yaml=AegisLab/data/initial_data/prod/otel-demo.yaml \
  --set-file initialDataFiles.ts_yaml=AegisLab/data/initial_data/prod/ts.yaml \
  > /tmp/aegislab-backend-overlay/base.yaml
```

Create the local-only Kustomize overlay that patches the producer into the single-pod `both` mode used for this issue, keeps the acceptance label selector `app=aegislab-backend`, and replaces the internal-only init path with a no-op:

```bash
cat >/tmp/aegislab-backend-overlay/kustomization.yaml <<'EOF'
resources:
  - base.yaml
patches:
  - path: producer-patch.yaml
  - path: producer-service-patch.yaml
  - path: consumer-scale-patch.yaml
EOF

cat >/tmp/aegislab-backend-overlay/producer-patch.yaml <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: aegislab-backend-producer
spec:
  selector:
    matchLabels:
      app: aegislab-backend
  template:
    metadata:
      labels:
        app: aegislab-backend
    spec:
      containers:
        - name: exp
          image: aegislab-backend:local
          imagePullPolicy: IfNotPresent
          command:
            - /app/entrypoint.sh
            - both
            - "8080"
          ports:
            - containerPort: 8080
            - containerPort: 4319
          livenessProbe:
            httpGet:
              path: /health
              port: 4319
          readinessProbe:
            httpGet:
              path: /health
              port: 4319
          volumeMounts:
            - name: experiment-storage
              mountPath: /var/lib/rcabench/experiment_storage
      initContainers:
        - name: wait-for-dependencies
          image: busybox:1.35
        - name: init-etcd-data
          image: busybox:1.35
          command:
            - sh
            - -c
            - echo Skipping etcd seed for local kind deploy; exit 0
      volumes:
        - name: experiment-storage
          persistentVolumeClaim:
            claimName: aegislab-backend-juicefs-experiment-storage
EOF

cat >/tmp/aegislab-backend-overlay/producer-service-patch.yaml <<'EOF'
apiVersion: v1
kind: Service
metadata:
  name: aegislab-backend-exp
spec:
  selector:
    app: aegislab-backend
EOF

cat >/tmp/aegislab-backend-overlay/consumer-scale-patch.yaml <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: aegislab-backend-consumer
spec:
  replicas: 0
EOF
```

Apply the overlay:

```bash
kubectl apply -k /tmp/aegislab-backend-overlay
```

Observed apply result:

```text
configmap/aegislab-backend-rcabench-config configured
configmap/aegislab-backend-etcd-producer-config configured
configmap/aegislab-backend-etcd-consumer-config configured
configmap/aegislab-backend-initial-data configured
service/aegislab-backend-exp configured
deployment.apps/aegislab-backend-producer configured
deployment.apps/aegislab-backend-consumer configured
statefulset.apps/aegislab-backend-etcd unchanged
statefulset.apps/aegislab-backend-jaeger unchanged
statefulset.apps/aegislab-backend-mysql unchanged
statefulset.apps/aegislab-backend-redis unchanged
```

### 3. Create the missing local-path StorageClass alias

The issue-specific local overlay expected `storageClassName: local-path`, but this cluster only had the default `standard` class backed by the same provisioner.

```bash
kubectl apply -f - <<'EOF'
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: local-path
provisioner: rancher.io/local-path
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
EOF
```

### 4. Create the ServiceAccount in `default` and bind it

This was required on the first run before the complete overlay above existed. The live backend resources were already in `default`, but the earlier chart render had created the ServiceAccount and ClusterRoleBinding subject in `exp`.

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: aegislab-backend-sa
  namespace: default
EOF

kubectl patch clusterrolebinding aegislab-backend-rcabench-rolebinding \
  --type='json' \
  -p='[{"op":"add","path":"/subjects/-","value":{"kind":"ServiceAccount","name":"aegislab-backend-sa","namespace":"default"}}]'
```

### 5. Replace private image references with public/local ones

The checked-in manifests and chart path still referenced internal `pair/...` images. These replacements were sufficient for local startup:

```bash
kubectl set image statefulset/aegislab-backend-etcd -n default \
  etcd=quay.io/coreos/etcd:v3.6.7
kubectl set image statefulset/aegislab-backend-redis -n default \
  redis=redis:8.0-M02-alpine3.20
kubectl set image statefulset/aegislab-backend-mysql -n default \
  mysql=mysql:8.0.43
kubectl set image statefulset/aegislab-backend-jaeger -n default \
  jaeger=jaegertracing/all-in-one:latest
kubectl delete pod -n default \
  aegislab-backend-etcd-0 \
  aegislab-backend-redis-0 \
  aegislab-backend-mysql-0 \
  aegislab-backend-jaeger-0
```

### 6. Patch the backend pod into a single-pod `both` mode local overlay

This issue-specific overlay kept the label selector required by the acceptance criteria (`app=aegislab-backend`) and made the liveness/readiness probe target `/health` on port `4319`.

```bash
kubectl set image deployment/aegislab-backend-producer -n default \
  exp=aegislab-backend:local

kubectl patch deployment aegislab-backend-producer -n default --type='json' -p='[
  {"op":"replace","path":"/spec/template/spec/containers/0/command","value":["/app/entrypoint.sh","both","8080"]},
  {"op":"replace","path":"/spec/template/spec/containers/0/livenessProbe/httpGet/path","value":"/health"},
  {"op":"replace","path":"/spec/template/spec/containers/0/livenessProbe/httpGet/port","value":4319},
  {"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/httpGet/path","value":"/health"},
  {"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/httpGet/port","value":4319},
  {"op":"add","path":"/spec/template/spec/containers/0/ports/-","value":{"containerPort":4319}},
  {"op":"replace","path":"/spec/template/spec/initContainers/0/image","value":"busybox:1.35"},
  {"op":"replace","path":"/spec/template/spec/initContainers/1/image","value":"busybox:1.35"},
  {"op":"replace","path":"/spec/template/spec/initContainers/1/command","value":["sh","-c","echo Skipping etcd seed for local kind deploy; exit 0"]},
  {"op":"add","path":"/spec/template/spec/containers/0/volumeMounts/-","value":{"mountPath":"/var/lib/rcabench/experiment_storage","name":"experiment-storage"}},
  {"op":"add","path":"/spec/template/spec/volumes/-","value":{"name":"experiment-storage","persistentVolumeClaim":{"claimName":"aegislab-backend-juicefs-experiment-storage"}}}
]'
```

### 7. Replace the empty initial-data ConfigMap with the checked-in prod seed files

The first startup failed because the `aegislab-backend-initial-data` ConfigMap had been created with empty strings. Producer initialization requires the real seed files.

```bash
kubectl create configmap aegislab-backend-initial-data -n default \
  --from-file=data.yaml=AegisLab/data/initial_data/prod/data.yaml \
  --from-file=otel-demo.yaml=AegisLab/data/initial_data/prod/otel-demo.yaml \
  --from-file=ts.yaml=AegisLab/data/initial_data/prod/ts.yaml \
  --dry-run=client -o yaml | kubectl apply -f -
```

### 8. Restart and verify

```bash
kubectl rollout restart deployment/aegislab-backend-producer -n default
kubectl rollout status deployment/aegislab-backend-producer -n default --timeout=300s
kubectl get pods -n default -l app=aegislab-backend
kubectl describe pod -n default aegislab-backend-producer-869b496f-gcxhf
```

Observed result:

```text
NAME                                       READY   STATUS    RESTARTS   AGE
aegislab-backend-producer-869b496f-gcxhf   1/1     Running   0          50s
```

The successful `kubectl describe pod` output showed:

```text
Labels:           app=aegislab-backend
...
State:          Running
...
Liveness:       http-get http://:4319/health
Readiness:      http-get http://:4319/health
```

## Health Check

Port-forward the in-cluster service:

```bash
kubectl port-forward -n default svc/aegislab-backend-exp 14319:4319
```

Then probe the liveness endpoint:

```bash
curl -i http://127.0.0.1:14319/health
```

Observed response:

```text
HTTP/1.1 200 OK
Content-Type: application/json

{"errors_total":0,"published_total":0,"received_total":0,"status":"healthy"}
```

## Hardcoded IPs And Override Method

The deployer-visible `10.10.10.*` values found during this pass were:

| Original value | Source | Override method used here | Final value used for local deploy |
| --- | --- | --- | --- |
| `http://10.10.10.161:8082` | [AegisLab/src/config.dev.toml](/home/ddq/AoyangSpace/aegis/AegisLab/src/config.dev.toml) `k8s.service.internal_url` | ConfigMap replacement in the applied deployment overlay | `http://aegislab-backend-exp:8080` |
| `http://10.10.10.161:3100` | [AegisLab/src/config.dev.toml](/home/ddq/AoyangSpace/aegis/AegisLab/src/config.dev.toml) `loki.address` | ConfigMap replacement in the applied deployment overlay | not needed for this pass because the local `/health` probe terminates on `4319`; the chart-owned replacement would be `http://<release>-loki:3100` |
| `10.10.10.240/library/busybox:1.35` | [AegisLab/src/config.dev.toml](/home/ddq/AoyangSpace/aegis/AegisLab/src/config.dev.toml) `k8s.init_container.busybox_image` | Deployment patch plus ConfigMap replacement | `busybox:1.35` |
| `10.10.10.240` registry assumptions | [AegisLab/helm/values.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/helm/values.yaml) `harbor.registry`, plus internal `pair/...` image defaults | StatefulSet/Deployment image replacement | `quay.io/coreos/etcd:v3.6.7`, `redis:8.0-M02-alpine3.20`, `mysql:8.0.43`, `jaegertracing/all-in-one:latest`, `aegislab-backend:local` |
| `redis://10.10.10.119:6379/1` | [AegisLab/helm/values.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/helm/values.yaml) `juicefs.metaurl` | Avoided by local PVC/storage-class workaround instead of in-cluster JuiceFS | not used in this local pass |

## Notes

- This pass used `default` for the live resources because that was the namespace of the existing issue-specific backend objects already present in the cluster.
- The chart-native path was not sufficient on its own for local kind. The working path required runtime overrides for namespace alignment, image sources, storage class naming, and initial seed data.
- The acceptance-selector form worked exactly as required:

```bash
kubectl get pods -n default -l app=aegislab-backend
```
