#!/bin/bash
set -e

CHART_DIR="helm"
REPO_NAME="aegislab"
REPO_URL="https://operationspai.github.io/aegislab/"

helm dependency update $CHART_DIR

mkdir -p .deploy
helm package $CHART_DIR -d .deploy

cd .deploy
if [ -f index.yaml ]; then
    helm repo index . --url $REPO_URL --merge index.yaml
else
    helm repo index . --url $REPO_URL
fi
cd ..