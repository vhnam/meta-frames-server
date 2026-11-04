#!/usr/bin/env bash
# Tests, builds and pushes the committed code, deploys it to Cloud Run, and checks /health.
#
#   scripts/deploy.sh [--yes]
#
# The new revision applies pending migrations to the production database on startup.
# Env, secrets, ports and volumes stay as they are on the service.
# Override the target with GCP_PROJECT, GCP_REGION, SERVICE or IMAGE_REPOSITORY.
set -euo pipefail

cd "$(dirname "$0")/.."

project="${GCP_PROJECT:-meta-frames}"
region="${GCP_REGION:-asia-southeast1}"
service="${SERVICE:-meta-frames-api}"
repository="${IMAGE_REPOSITORY:-$region-docker.pkg.dev/$project/meta-frames/api}"
confirmed="${1:-}"

fail() {
  echo "error: $1" >&2
  exit 1
}

# An image built from a dirty tree cannot be traced to a commit.
if [ -n "$(git status --porcelain)" ]; then
  fail "the working tree has uncommitted changes; commit them first"
fi

commit="$(git rev-parse --short HEAD)"
image="$repository:$commit-$(date +%Y%m%d%H%M%S)"

if [ -z "$(git branch -r --contains HEAD)" ]; then
  echo "warning: commit $commit is not on origin"
fi

echo "Commit:  $(git log -1 --format='%h %s')"
echo "Image:   $image"
echo "Service: $service ($project, $region)"
echo "Startup applies any pending migrations to production. A traffic rollback does not undo them."

if [ "$confirmed" != "--yes" ]; then
  printf 'Deploy? Type "yes": '
  read -r answer
  [ "$answer" = "yes" ] || fail "cancelled"
fi

go test ./...

docker build --platform linux/amd64 -f build/package/Dockerfile -t "$image" .
docker push "$image"

gcloud run deploy "$service" --project "$project" --region "$region" --image "$image" --quiet

service_url="$(gcloud run services describe "$service" --project "$project" --region "$region" --format='value(status.url)')"

for attempt in 1 2 3 4 5; do
  if curl -fsS --max-time 20 "$service_url/health"; then
    echo
    echo "Deployed $image"
    exit 0
  fi
  echo "health check $attempt failed"
  sleep 5
done

fail "$service_url/health did not return success; check the Cloud Run logs"
