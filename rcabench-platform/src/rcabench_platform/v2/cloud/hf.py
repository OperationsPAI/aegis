import functools
from pathlib import Path

from huggingface_hub import HfApi

from ..logging import timeit
from ..utils.fmap import fmap_threadpool
from .spec import Storage


class HuggingFaceStorage(Storage):
    def __init__(
        self,
        local_root: Path,
        hf_client: HfApi,
        repo_id: str,
        concurrent_upload: int = 0,
        concurrent_download: int = 0,
    ) -> None:
        assert local_root.is_dir()
        self._local_root = local_root

        self._hf = hf_client
        self._repo_id = repo_id

        self._concurrent_upload = concurrent_upload
        self._concurrent_download = concurrent_download

    def _upload_folder(self, folder_name: Path, commit_message: str) -> None:
        folder_path = self._local_root / folder_name
        assert folder_path.is_dir()

        self._hf.upload_folder(
            repo_id=self._repo_id,
            repo_type="dataset",
            path_in_repo=str(folder_name),
            folder_path=str(folder_path),
            commit_message=commit_message,
        )

    def _download_folder(self, folder_name: Path) -> None:
        folder_path = self._local_root / folder_name
        assert not folder_path.exists()

        self._hf.snapshot_download(
            repo_id=self._repo_id,
            repo_type="dataset",
            local_dir=self._local_root,
            allow_patterns=[str(f"{folder_name}/*")],
            max_workers=self._concurrent_download,
        )

    @timeit()
    def upload_meta(self, dataset: str) -> None:
        self._upload_folder(
            folder_name=Path("meta") / dataset,
            commit_message=f"upload meta `{dataset}`",
        )

    @timeit()
    def upload_datapack(self, dataset: str, datapack: str) -> None:
        self._upload_folder(
            folder_name=Path("data") / dataset / datapack,
            commit_message=f"upload datapack `{dataset}/{datapack}`",
        )

    @timeit()
    def upload_dataset(self, dataset: str) -> None:
        dataset_path = self._local_root / "data" / dataset
        assert dataset_path.is_dir()

        tasks = []
        for datapack_path in dataset_path.iterdir():
            assert datapack_path.is_dir()
            datapack = datapack_path.name
            tasks.append(functools.partial(self.upload_datapack, dataset, datapack))

        fmap_threadpool(tasks, parallel=self._concurrent_upload)

        self.upload_meta(dataset)

    @timeit()
    def download_meta(self, dataset: str) -> None:
        self._download_folder(folder_name=Path("meta") / dataset)

    @timeit()
    def download_datapack(self, dataset: str, datapack: str) -> None:
        self._download_folder(folder_name=Path("data") / dataset / datapack)

    @timeit()
    def download_dataset(self, dataset: str) -> None:
        self._hf.snapshot_download(
            repo_id=self._repo_id,
            repo_type="dataset",
            local_dir=self._local_root,
            allow_patterns=[f"data/{dataset}/*"],
            max_workers=self._concurrent_download,
        )

        self.download_meta(dataset)
