# Development Guide

## Requirements

### Operating System

This project is primarily developed and tested on **Ubuntu 24.04 LTS** or later versions. Other Linux distributions and macOS environments should be compatible with minimal configuration adjustments.

Windows is not officially supported. While some functionality may work in Windows environments (especially through WSL), we cannot guarantee full compatibility or provide dedicated support.

### Toolchain

|                     Toolchain                      | Version |
| :------------------------------------------------: | :-----: |
|          [uv](https://docs.astral.sh/uv)           | ^0.7.13 |
|       [just](https://github.com/casey/just)        | ^1.21.0 |
|  [Docker Engine](https://docs.docker.com/engine/)  |    *    |
| [Docker Compose](https://docs.docker.com/compose/) |    *    |

### IDE

Recommended setup

+ [Visual Studio Code](https://code.visualstudio.com/) with the following extensions:
    + [Python Extension Pack](https://marketplace.visualstudio.com/items?itemName=donjayamanne.python-extension-pack)
    + [Pylance](https://marketplace.visualstudio.com/items?itemName=ms-python.vscode-pylance)
    + [Ruff](https://marketplace.visualstudio.com/items?itemName=charliermarsh.ruff)
    + [Git Graph](https://marketplace.visualstudio.com/items?itemName=mhutchie.git-graph)
    + [Data Wrangler](https://marketplace.visualstudio.com/items?itemName=ms-toolsai.datawrangler)

## Get Started

### Download Source Code

```bash
git clone git@github.com:LGU-SE-Internal/rcabench-platform.git
cd rcabench-platform
```

### Install Dependencies

```bash
uv sync --all-extras
```

### Run Basic Checks

```bash
just dev
```

### Local Services

We have the following localhost services running in the background:
+ [neo4j](https://neo4j.com/): for graph visualization

Start the services:

```bash
docker compose up -d
```

Stop the services:

```bash
docker compose down
```

### Link Datasets

Mount JuiceFS to your machine:

```bash
sudo juicefs mount redis://10.10.10.119:6379/1 /mnt/jfs -d --cache-size=1024
```

See [infra/README.md](infra/README.md) for more details.

Link the datasets to the project directory:

```bash
mkdir -p data
cd data

ln -s /mnt/jfs/rcabench-platform-v2 ./
ln -s /mnt/jfs/rcabench_dataset ./
# ... other datasets
```

### Run Self Test

```bash
./main.py self test
```

If the output shows that all tests passed, then your environment is ready for development.

## Git

### Commit Message

We follow the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) specification.

### Branching Strategy

When you are developing a new feature, create a new branch from `main` and name it according to the following convention:

```
{your github id}/feat/{feature-name}
```

This branch prefixed with your github id is **your own** working branch. You can force-push to it freely. Do anything you want in this branch.

When you are done with the feature, **create a pull request** to `main` and invite other developers to review your code. If the code is approved, it will be merged into `main`. Then you can start a new branch from `main` and continue your work.

The `main` branch is the default branch for this repository. `main` is protected and should not be used for development. Before merging any changes into `main`, ensure that the following conditions are met:
+ The [basic checks](#run-basic-checks) passed successfully.
+ The branch is up to date with `main`.
+ The branch is free of merge conflicts.
+ **The changes will not break other developers' workflow.**

We requires a **linear commit history**. Please use `git rebase` to keep your branch up to date with `main`. When merging your branch into `main`, use `git merge --ff-only` to ensure a fast-forward merge. This will keep the commit history clean and linear.
