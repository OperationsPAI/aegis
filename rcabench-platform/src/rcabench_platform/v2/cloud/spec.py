import subprocess
import tempfile
from abc import ABC, abstractmethod
from collections.abc import Generator
from contextlib import contextmanager
from pathlib import Path


class Storage(ABC):
    @abstractmethod
    def upload_meta(self, dataset: str) -> None: ...

    @abstractmethod
    def upload_datapack(self, dataset: str, datapack: str) -> None: ...

    @abstractmethod
    def upload_dataset(self, dataset: str) -> None: ...

    @abstractmethod
    def download_meta(self, dataset: str) -> None: ...

    @abstractmethod
    def download_datapack(self, dataset: str, datapack: str) -> None: ...

    @abstractmethod
    def download_dataset(self, dataset: str) -> None: ...


@contextmanager
def zipped_folder(folder_path: Path, zip_name: str) -> Generator[tuple[Path, Path], None, None]:
    assert folder_path.is_dir()
    assert folder_path.parent.is_dir()

    with tempfile.TemporaryDirectory() as tmpdir:
        tmp_path = Path(tmpdir)
        zip_path = tmp_path / f"{zip_name}.zip"

        subprocess.run(
            ["zip", "-r", str(zip_path), str(folder_path.name)],
            cwd=folder_path.parent,
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )

        sha256_path = zip_path.with_suffix(".sha256")
        subprocess.run(
            ["sha256sum", str(zip_path.name)],
            cwd=tmp_path,
            check=True,
            stdout=sha256_path.open("w"),
            stderr=subprocess.DEVNULL,
        )

        yield zip_path, sha256_path


@contextmanager
def unzipped_folder(zip_path: Path) -> Generator[Path, None, None]:
    assert zip_path.is_file()

    with tempfile.TemporaryDirectory() as tmpdir:
        tmpdir = Path(tmpdir)

        subprocess.run(
            ["unzip", str(zip_path)],
            cwd=str(tmpdir),
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )

        folder = tmpdir / zip_path.stem
        assert folder.is_dir()

        yield folder
