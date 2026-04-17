# 05. AegisLab Frontend

## Host-side install and build

Executed:

```bash
cd AegisLab-frontend
pnpm install
pnpm build
```

Observed behavior:
- `pnpm install` succeeded on this machine
- both commands warned that `.npmrc` referenced `${NPM_TOKEN}` without an exported env var
- success here should not be treated as a fresh-machine guarantee because local package cache/store state may have masked an auth failure

Captured warning:

```text
WARN Issue while reading ".../AegisLab-frontend/.npmrc". Failed to replace env in config: ${NPM_TOKEN}
```

## Docker image build without secret

Executed:

```bash
cd AegisLab-frontend
docker build -t aegis-frontend-test .
```

Captured failure:

```text
#11 0.185 cat: can't open '/run/secrets/NPM_TOKEN': No such file or directory
#11 2.895 ERR_PNPM_FETCH_401 GET https://npm.pkg.github.com/download/@OperationsPAI/client/1.2.0/...: Unauthorized - 401
ERROR: failed to solve: process "/bin/sh -c export NPM_TOKEN=$(cat /run/secrets/NPM_TOKEN) && pnpm install --no-frozen-lockfile" did not complete successfully: exit code: 1
```

This is the strongest reproducible evidence in this pass that a valid `NPM_TOKEN` is required for clean containerized builds.

## Repo config notes

Observed in [AegisLab-frontend/vite.config.ts](/home/ddq/AoyangSpace/aegis/AegisLab-frontend/vite.config.ts):

- dev proxy default is `http://127.0.0.1:8082`
- `VITE_API_TARGET` can override that cleanly

Note:
- [AegisLab-frontend/README.md](/home/ddq/AoyangSpace/aegis/AegisLab-frontend/README.md) still says the dev proxy targets `http://10.10.10.220:32080`
- the checked-in Vite config now defaults to `http://127.0.0.1:8082`
- a contributor following the README literally will point the frontend at the wrong backend unless they inspect `vite.config.ts`

Observed in [AegisLab-frontend/.npmrc](/home/ddq/AoyangSpace/aegis/AegisLab-frontend/.npmrc):

```text
@OperationsPAI:registry=https://npm.pkg.github.com/
//npm.pkg.github.com/:_authToken=${NPM_TOKEN}
```

## Verification commands once backend and cluster exist

Local dev server:

```bash
cd AegisLab-frontend
VITE_API_TARGET=http://127.0.0.1:8082 pnpm dev
```

Container build:

```bash
cd AegisLab-frontend
docker build \
  --secret id=NPM_TOKEN,env=NPM_TOKEN \
  -t aegis-frontend-test .
```

Expected:
- dev server starts on `http://localhost:3000`
- `/api` proxy reaches the backend
- container build succeeds without `ERR_PNPM_FETCH_401`
