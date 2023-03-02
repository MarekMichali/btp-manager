#!/usr/bin/env bash

# standard bash error handling
set -o nounset  # treat unset variables as an error and exit immediately.
set -o errexit  # exit immediately when a command fails.
set -E          # needs to be set if we want the ERR trap
set -o pipefail # prevents errors in a pipeline from being masked

# This script has the following argument: new release tag
#
# ./create_changelog.sh 1.1.0

# Expected variables:
#             GITHUB_TOKEN - github authorization token

REPOSITORY="MarekMichali/btp-manager"
RELEASE_TAG=$1
CHANGELOG_FILENAME="CHANGELOG.md"
LATEST_RELEASE_TAG=$(curl -H "Authorization: token $GITHUB_TOKEN" https://api.github.com/repos/$REPOSITORY/releases/latest | jq -r '.tag_name')

# temp files
OLD_CONTRIB=$$.old
REL_CONTRIB=$$.rel
NEW_CONTRIB=$$.new

# TODO use %al?
echo "## What's changed" >> ${CHANGELOG_FILENAME}

git log ${LATEST_RELEASE_TAG}..HEAD --pretty=format:"* %s by @%an" | grep -v "^$" >> ${CHANGELOG_FILENAME}
git log ${LATEST_RELEASE_TAG} --pretty=format:"%an"|sort -u > ${OLD_CONTRIB}
git log ${LATEST_RELEASE_TAG}..HEAD --pretty=format:"%an"|sort -u > ${REL_CONTRIB}

join -v2 ${OLD_CONTRIB} ${REL_CONTRIB} >${NEW_CONTRIB}

if [ -s ${NEW_CONTRIB} ]
then
  echo -e "\n## New contributors" >> ${CHANGELOG_FILENAME}
  while read -r user
  do
    REF_PR=$(grep "@$user" ${CHANGELOG_FILENAME} | head -1 | grep -o " (#[0-9]\+)" || true)
    if [ -n "${REF_PR}" ] #reference found
    then
      REF_PR=" in ${REF_PR}"
    fi
    echo "* @$user made first contribution${REF_PR}" >> ${CHANGELOG_FILENAME}
  done <${NEW_CONTRIB}
fi

echo -e "\n**Full changelog**: https://github.com/$REPOSITORY/compare/$(git rev-list --max-parents=0 HEAD)...${RELEASE_TAG}" >> ${CHANGELOG_FILENAME}

# cleanup
rm ${OLD_CONTRIB} ${NEW_CONTRIB} ${REL_CONTRIB} || echo "cleaned up"
