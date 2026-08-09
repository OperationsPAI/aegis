#!/bin/bash -ex
git checkout main
[[ -z "$(git status -s)" ]]

uv version --bump patch
VERSION="v$(uv version --short)"

git add -A
git commit -m "release $VERSION"
git push origin main

git tag "$VERSION"
git push origin "$VERSION"
