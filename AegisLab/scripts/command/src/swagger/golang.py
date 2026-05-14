import json
import os
import shutil
import sys
from pathlib import Path

from python_on_whales import docker

from src.common.command import run_command
from src.common.common import PROJECT_ROOT, console, settings
from src.swagger.common import SWAGGER_ROOT


class GoSDK:
    """
    Generate a typed Go API client + DTOs from the unified OpenAPI 3 spec.

    Output goes to `src/cli/apiclient/` (inside the `aegis` Go module) so the
    aegisctl CLI can import it directly without module-wrangling. The generator
    is the same `openapi-generator-cli` Docker image used for python/typescript
    targets — `-g go` emits a client SDK with per-operation methods + DTO
    structs that mirror the spec components.
    """

    SDK_DIR = PROJECT_ROOT / "src" / "cli" / "apiclient"
    SDK_GEN_DIR = PROJECT_ROOT / "src" / "cli" / "apiclient-gen"
    GENERATOR_CONFIG_DIR = PROJECT_ROOT / ".openapi-generator" / "golang"

    def __init__(self, version: str) -> None:
        self.version = version

    def generate(self) -> None:
        # 1. Update generator config with the requested version.
        sdk_config = self.GENERATOR_CONFIG_DIR / "config.json"
        with open(sdk_config) as f:
            config_data = json.load(f)

        config_data["packageVersion"] = self.version

        tmp_sdk_config = self.GENERATOR_CONFIG_DIR / "config_tmp.json"
        with open(tmp_sdk_config, "w") as f:
            json.dump(config_data, f, indent=2)

        console.print(
            f"[bold green]✅ Updated packageVersion to {self.version}[/bold green]"
        )

        # 2. Generate via openapi-generator-cli in Docker.
        console.print("[bold blue]Step 1: Generating Go client SDK...[/bold blue]")

        if self.SDK_GEN_DIR.exists():
            shutil.rmtree(self.SDK_GEN_DIR)
        self.SDK_GEN_DIR.mkdir(parents=True)

        volume_path = Path(settings.openapi.generator_volume_root)
        relative_swagger = SWAGGER_ROOT.relative_to(PROJECT_ROOT)
        relative_sdk_gen = self.SDK_GEN_DIR.relative_to(PROJECT_ROOT)
        relative_generator_config = self.GENERATOR_CONFIG_DIR.relative_to(PROJECT_ROOT)

        # Use the same audience-filtered spec the python SDK uses (`sdk.json`)
        # so the Go client and Python SDK stay in lockstep on what's exposed.
        container_input_path = (
            volume_path / relative_swagger / "converted" / "sdk.json"
        )
        container_output_path = volume_path / relative_sdk_gen
        container_config_path = (
            volume_path / relative_generator_config / "config_tmp.json"
        )

        current_user = os.getuid()
        current_group = os.getgid()

        try:
            docker.run(
                settings.generator_image,
                command=[
                    "generate",
                    "-i",
                    container_input_path.as_posix(),
                    "-g",
                    "go",
                    "-o",
                    container_output_path.as_posix(),
                    "-c",
                    container_config_path.as_posix(),
                ],
                volumes=[(PROJECT_ROOT, volume_path)],
                user=f"{current_user}:{current_group}",
                remove=True,
            )
        except Exception as e:
            console.print(
                f"[bold red]❌ Error during Go SDK generation: {e}[/bold red]"
            )
            sys.exit(1)
        finally:
            if tmp_sdk_config.exists():
                tmp_sdk_config.unlink(missing_ok=True)

        console.print(
            "[bold green]✅ Generated Go client SDK successfully![/bold green]"
        )

        # 3. Swap generated tree into the live location.
        console.print(
            "[bold blue]Step 2: Installing generated client into src/cli/apiclient/...[/bold blue]"
        )
        if self.SDK_DIR.exists():
            shutil.rmtree(self.SDK_DIR)
        shutil.copytree(self.SDK_GEN_DIR, self.SDK_DIR)

        # 4. Drop generator-bundled files that conflict with the surrounding
        # Go module: standalone go.mod / go.sum / .openapi-generator-ignore /
        # .gitignore would shadow the parent module.
        for noisy in ("go.mod", "go.sum", ".openapi-generator-ignore", ".gitignore"):
            target = self.SDK_DIR / noisy
            if target.exists():
                target.unlink(missing_ok=True)

        if self.SDK_GEN_DIR.exists():
            shutil.rmtree(self.SDK_GEN_DIR)

        # 5. Format with the same toolchain the rest of the Go code uses.
        console.print("[bold blue]Step 3: Formatting generated Go code...[/bold blue]")
        if shutil.which("gofmt"):
            run_command(["gofmt", "-w", self.SDK_DIR.as_posix()])
            console.print("[dim]✓ gofmt applied[/dim]")

        console.print(
            f"[bold green]✅ Go client installed at {self.SDK_DIR.relative_to(PROJECT_ROOT)}[/bold green]"
        )
