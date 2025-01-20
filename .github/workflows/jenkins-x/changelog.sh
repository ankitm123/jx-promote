#!/usr/bin/env sh

echo "REPO_NAME = $PULL_BASE_SHA"

export PULL_BASE_SHA=$(git rev-parse HEAD)

if [ -d "charts/$REPO_NAME" ]; then
  sed -i -e "s/^version:.*/version: $VERSION/" ./charts/$REPO_NAME/Chart.yaml
  sed -i -e "s/tag:.*/tag: $VERSION/" ./charts/$REPO_NAME/values.yaml;

  # sed -i -e "s/repository:.*/repository: $DOCKER_REGISTRY\/$DOCKER_REGISTRY_ORG\/$APP_NAME/" ./charts/$REPO_NAME/values.yaml
else
  echo no charts
fi

# See https://github.com/actions/checkout/issues/766
git config --global --add safe.directory "$GITHUB_WORKSPACE"
jx changelog create --verbose --version=$VERSION --rev=$PULL_BASE_SHA --output-markdown=changelog.md
