#!/usr/bin/env -S uv run -s
from pathlib import Path

from rcabench.openapi import (
    CreateDatasetReq,
    CreateDatasetVersionReq,
    DatasetsApi,
    InjectionsApi,
    LabelItem,
    SearchDatasetReq,
    SearchInjectionReq,
)

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.clients.rcabench_ import get_rcabench_client
from rcabench_platform.v3.sdk.datasets.rcabench import rcabench_split_train_test, valid
from rcabench_platform.v3.sdk.datasets.spec import get_datapack_list

DATASET_NAME = "pair-diag"


def get_previous_datapacks(datasets_api: DatasetsApi) -> list[str]:
    resp = datasets_api.search_datasets(request=SearchDatasetReq(name_pattern=DATASET_NAME, include_versions=True))
    assert resp.code is not None and resp.code < 300 and resp.data is not None and resp.data.items is not None
    if len(resp.data.items) == 0:
        return []
    dataset = resp.data.items[0]
    assert dataset.id is not None

    if dataset.versions is not None:
        version = dataset.versions[0]
        assert version.id is not None

        version_resp = datasets_api.get_dataset_version_by_id(dataset_id=dataset.id, version_id=version.id)
        assert version_resp.code is not None and version_resp.code < 300 and version_resp.data is not None

        datapacks = version_resp.data.datapacks
        assert datapacks is not None

        previous_datapacks = [datapack.name for datapack in datapacks if datapack.name is not None]
        assert len(previous_datapacks) > 0
        return previous_datapacks
    return []


def split_datapacks(datapacks: list, previous_datapacks: list[str]) -> tuple[list, list]:
    train_datapacks, test_datapacks = rcabench_split_train_test(
        src_folder="rcabench_dataset",
        datapacks=datapacks[:300],
        train_ratio=0.8,
        previous_datapacks=previous_datapacks,
    )
    return train_datapacks, test_datapacks


def create_dataset(
    datasets_api: DatasetsApi,
    name: str,
    dataset_type: str,
    version: str,
    description: str,
    datapacks: list,
) -> bool:
    resp = datasets_api.create_dataset(
        request=CreateDatasetReq(
            name=name,
            type=dataset_type,
            description=description,
            version=CreateDatasetVersionReq(
                name=version,
                datapacks=datapacks,
            ),
        ),
    )

    logger.info(f"request dataset creation: code[{resp.code}], message[{resp.message}]")
    if resp.code is None or resp.code == 201:
        return False

    assert resp.data is not None and resp.data.id is not None and resp.data.name is not None
    logger.info(f"dataset created: id[{resp.data.id}], name[{resp.data.name}]")
    return True


@app.command()
@timeit()
def run(stage: int):
    datapacks = get_datapack_list("rcabench")  # can be replaced by query with issues

    client = get_rcabench_client()
    datasets_api = DatasetsApi(client)

    previous_datapacks = get_previous_datapacks(datasets_api)

    train_datapacks, test_datapacks = rcabench_split_train_test(
        src_folder="rcabench_dataset",
        datapacks=datapacks,
        train_ratio=0.8,
        previous_datapacks=previous_datapacks,
        datapack_limit=300,
    )

    create_dataset(
        datasets_api=datasets_api,
        name=DATASET_NAME,
        dataset_type="train",
        version=f"train-stage-{stage}",
        description=f"training dataset for pair-diag-stage-{stage}, bootstrap dataset",
        datapacks=train_datapacks,
    )

    create_dataset(
        datasets_api=datasets_api,
        name=DATASET_NAME,
        dataset_type="test",
        version=f"test-stage-{stage}",
        description=f"test dataset for pair-diag-stage-{stage}, bootstrap dataset",
        datapacks=test_datapacks,
    )


def get_datapack(tags: list[str] | None = None) -> list[str]:
    client = get_rcabench_client()
    api = InjectionsApi(client)

    search_req = SearchInjectionReq()
    if tags is not None and len(tags) > 0:
        search_req.include_labels = True
        search_req.labels = [LabelItem(key="tag", value=tag) for tag in tags]

    resp = api.search_injections(search=SearchInjectionReq())
    assert resp.code is not None and resp.code < 300 and resp.data is not None and resp.data.items is not None
    logger.info(f"found {len(resp.data.items)} injections")

    res: list[str] = []
    if tags is None:
        res = [
            item.name
            for item in resp.data.items
            if item.name is not None and valid(Path("data") / "rcabench_dataset" / item.name)
        ]
    else:
        tag_set = set(tags)
        tag_injections: dict[str, list[str]] = {tag: [] for tag in tags}

        for item in resp.data.items:
            if item.labels is None or item.name is None:
                continue

            item_name = item.name
            item_path = Path("data") / "rcabench_dataset" / item_name
            if not valid(item_path):
                continue

            for label in item.labels:
                if label.key == "tag" and label.value in tag_set:
                    tag_name = label.value
                    tag_injections[tag_name].append(item_name)
                    break

        res = [injection for tag in tags for injection in tag_injections.get(tag, [])]

    return res


@app.command()
def build_anomaly(date: str, name: str):
    client = get_rcabench_client()
    datasets_api = DatasetsApi(client)

    datapacks = get_datapack(["absolute_anomaly"])

    create_dataset(
        datasets_api=datasets_api,
        name="pair-diag",
        dataset_type="all",
        version=f"all-{name}-{date}",
        description=f"all the {name} dataset until {date} for study",
        datapacks=datapacks,
    )

    train_datapacks, test_datapacks = rcabench_split_train_test(
        src_folder="rcabench_dataset",
        datapacks=datapacks,
        train_ratio=0.8,
        previous_datapacks=[],
        datapack_limit=len(datapacks),
    )

    create_dataset(
        datasets_api=datasets_api,
        name="pair-diag",
        dataset_type="train",
        version=f"study-{name}-train{date}",
        description="training dataset for study",
        datapacks=train_datapacks,
    )

    create_dataset(
        datasets_api=datasets_api,
        name="pair-diag",
        dataset_type="test",
        version=f"study-{name}-test{date}",
        description="test dataset for study",
        datapacks=test_datapacks,
    )


if __name__ == "__main__":
    app()
