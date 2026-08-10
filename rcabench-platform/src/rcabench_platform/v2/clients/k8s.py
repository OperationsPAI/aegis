from dataclasses import asdict, dataclass
from typing import Any

# https://github.com/kubernetes-client/python/issues/225
import kubernetes
import kubernetes.client
import kubernetes.config

from ..logging import logger, timeit


@dataclass(kw_only=True)
class KubeInfo:
    nodes: list[kubernetes.client.V1Node]

    pvs: list[kubernetes.client.V1PersistentVolume]

    namespaces: list[kubernetes.client.V1Namespace]

    services: dict[str, list[kubernetes.client.V1Service]]

    stateful_sets: dict[str, list[kubernetes.client.V1StatefulSet]]

    deployments: dict[str, list[kubernetes.client.V1Deployment]]

    replica_sets: dict[str, list[kubernetes.client.V1ReplicaSet]]

    pods: dict[str, list[kubernetes.client.V1Pod]]

    pvcs: dict[str, list[kubernetes.client.V1PersistentVolumeClaim]]

    def to_dict(self) -> dict[str, Any]:
        ans = asdict(self)
        for k, v in ans.items():
            if isinstance(v, list):
                ans[k] = [x.to_dict() for x in v]
            elif isinstance(v, dict):
                ans[k] = {k: [x.to_dict() for x in lst] for k, lst in v.items()}
        return ans


@timeit()
def download_kube_info(*, ns: str | None) -> KubeInfo:
    try:
        kubernetes.config.load_kube_config()
    except kubernetes.config.ConfigException:
        try:
            kubernetes.config.load_incluster_config()
        except kubernetes.config.ConfigException as e:
            logger.error("Could not configure kubernetes python client")
            raise e

    core_v1 = kubernetes.client.CoreV1Api()
    apps_v1 = kubernetes.client.AppsV1Api()

    nodes = core_v1.list_node().items

    pvs = core_v1.list_persistent_volume().items

    if ns:
        namespaces = [core_v1.read_namespace(ns)]
        ns_list = [ns]
    else:
        namespaces = core_v1.list_namespace().items
        ns_list = []
        for namespace in namespaces:
            assert namespace.metadata and namespace.metadata.name
            ns_list.append(namespace.metadata.name)

    services = {ns: core_v1.list_namespaced_service(ns).items for ns in ns_list}

    deployments = {ns: apps_v1.list_namespaced_deployment(ns).items for ns in ns_list}

    stateful_sets = {ns: apps_v1.list_namespaced_stateful_set(ns).items for ns in ns_list}

    replica_sets = {ns: apps_v1.list_namespaced_replica_set(ns).items for ns in ns_list}

    pods = {ns: core_v1.list_namespaced_pod(ns).items for ns in ns_list}

    pvcs = {ns: core_v1.list_namespaced_persistent_volume_claim(ns).items for ns in ns_list}

    return KubeInfo(
        nodes=nodes,
        pvs=pvs,
        namespaces=namespaces,  # type: ignore
        services=services,
        stateful_sets=stateful_sets,
        deployments=deployments,
        replica_sets=replica_sets,
        pods=pods,
        pvcs=pvcs,
    )
