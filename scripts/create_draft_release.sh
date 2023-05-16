#!/usr/bin/env bash

# This script returns the id of the draft release

# standard bash error handling
set -o nounset  # treat unset variables as an error and exit immediately.
set -o errexit  # exit immediately when a command fails.
set -E          # needs to be set if we want the ERR trap
set -o pipefail # prevents errors in a pipeline from being masked

CHANGELOG=$(cat CHANGELOG.md)

JSON_PAYLOAD=$(jq -n \
  --arg tag_name "v1.0.0" \
  --arg target_commitish "main" \
  --arg name "v1.0.0" \
  --arg body "$CHANGELOG" \
  '{
    "tag_name": $tag_name,
    "name": $name,
    "body": $body,
    "draft": true
  }')

CURL_RESPONSE=$(curl -L \
  -X POST \
  -H "Accept: application/vnd.github+json" \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  https://api.github.com/repos/MarekMichali/btp-manager/releases \
  -d "$JSON_PAYLOAD")

echo "$(echo $CURL_RESPONSE | jq -r ".id")"
