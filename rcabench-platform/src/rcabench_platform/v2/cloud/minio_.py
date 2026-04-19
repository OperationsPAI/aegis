import functools
import shutil
import subprocess
import tempfile
from pathlib import Path

import minio

from ..logging import timeit
from ..utils.fmap import fmap_threadpool
from .spec import Storage, unzipped_folder, zipped_folder


class MinioStorage(Storage):
    def __init__(
        self,
        *,
        local_root: Path,
        minio_client: minio.Minio,
        bucket: str,
        object_root: str,
        concurrent_upload: int = 0,
        concurrent_download: int = 0,
    ) -> None:
        assert local_root.is_dir()
        self._local_root = local_root

        assert minio_client.bucket_exists(bucket)
        self._minio = minio_client
        self._bucket = bucket
        self._object_root = object_root

        self._concurrent_upload = concurrent_upload
        self._concurrent_download = concurrent_download

    def _get_meta_key(self, dataset: str) -> str:
        key = f"meta/{dataset}"
        if self._object_root:
            key = f"{self._object_root}/{key}"
        return key

    def _get_datapack_key(self, dataset: str, datapack: str) -> str:
        key = f"data/{dataset}/{datapack}"
        if self._object_root:
            key = f"{self._object_root}/{key}"
        return key

    def _get_meta_path(self, dataset: str) -> Path:
        return self._local_root / "meta" / dataset

    def _get_datapack_path(self, dataset: str, datapack: str) -> Path:
        return self._local_root / "data" / dataset / datapack

    def _upload_folder(self, folder_path: Path, object_key: str):
        with zipped_folder(folder_path, folder_path.name) as (zip_path, sha256_path):
            self._minio.fput_object(
                bucket_name=self._bucket,
                object_name=object_key + ".zip",
                file_path=str(zip_path),
            )
            self._minio.fput_object(
                bucket_name=self._bucket,
                object_name=object_key + ".sha256",
                file_path=str(sha256_path),
            )

    def _download_folder(self, folder_path: Path, object_key: str):
        folder_path.parent.mkdir(parents=True, exist_ok=True)
        assert not folder_path.exists()

        with tempfile.TemporaryDirectory() as tmpdir:
            tmpdir = Path(tmpdir)
            zip_path = tmpdir / f"{folder_path.name}.zip"
            sha256_path = tmpdir / f"{folder_path.name}.sha256"

            self._minio.fget_object(
                bucket_name=self._bucket,
                object_name=object_key + ".zip",
                file_path=str(zip_path),
            )
            self._minio.fget_object(
                bucket_name=self._bucket,
                object_name=object_key + ".sha256",
                file_path=str(sha256_path),
            )

            assert zip_path.is_file()
            assert sha256_path.is_file()

            subprocess.run(
                ["sha256sum", "-c", str(sha256_path.name)],
                cwd=tmpdir,
                check=True,
                # stdout=subprocess.DEVNULL,
                # stderr=subprocess.DEVNULL,
            )

            with unzipped_folder(zip_path) as zip_folder:
                shutil.move(zip_folder, folder_path)

    @timeit()
    def upload_meta(self, dataset: str) -> None:
        meta_folder = self._get_meta_path(dataset)
        meta_key = self._get_meta_key(dataset)
        self._upload_folder(meta_folder, meta_key)

    @timeit()
    def upload_datapack(self, dataset: str, datapack: str) -> None:
        datapack_folder = self._get_datapack_path(dataset, datapack)
        datapack_key = self._get_datapack_key(dataset, datapack)
        self._upload_folder(datapack_folder, datapack_key)

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
        meta_folder = self._get_meta_path(dataset)
        meta_key = self._get_meta_key(dataset)
        self._download_folder(meta_folder, meta_key)

    @timeit()
    def download_datapack(self, dataset: str, datapack: str) -> None:
        datapack_folder = self._get_datapack_path(dataset, datapack)
        datapack_key = self._get_datapack_key(dataset, datapack)
        self._download_folder(datapack_folder, datapack_key)

    @timeit()
    def download_dataset(self, dataset: str) -> None:
        if self._object_root:
            prefix = f"{self._object_root}/data/{dataset}/"
        else:
            prefix = f"data/{dataset}/"

        datapacks = []
        for object in self._minio.list_objects(self._bucket, prefix=prefix):
            key = object.object_name
            assert isinstance(key, str)

            assert key.startswith(prefix)

            if not key.endswith(".sha256"):
                continue

            datapack = key.removeprefix(prefix).removesuffix(".sha256")
            datapacks.append(datapack)

        dataset_path = self._local_root / "data" / dataset
        dataset_path.mkdir(exist_ok=True)

        tasks = []
        for datapack in datapacks:
            tasks.append(functools.partial(self.download_datapack, dataset, datapack))

        fmap_threadpool(tasks, parallel=self._concurrent_download)

        self.download_meta(dataset)
