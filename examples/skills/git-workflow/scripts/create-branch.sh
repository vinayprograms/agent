#!/bin/bash
# Create a new branch following naming conventions
TYPE="$1"
NAME="$2"

if [ -z "$TYPE" ] || [ -z "$NAME" ]; then
    echo "Usage: create-branch.sh <type> <name>"
    echo "Types: feature, fix, docs, refactor, test"
    exit 1
fi

BRANCH="${TYPE}/${NAME}"
git checkout -b "$BRANCH"
echo "Created branch: $BRANCH"
