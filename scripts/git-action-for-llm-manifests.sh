#!/bin/bash
# Update the RELEASE_BRANCH in the sync-main GitHub Action for llm-d-conformance-manifests.
# Usage: ./git-action-for-llm-manifests.sh <new-branch>
# Example: ./git-action-for-llm-manifests.sh 3.5-ea1

set -e

REPO="aneeshkp/llm-d-conformance-manifests"
REPO_DIR="$HOME/github.com/aneeshkp/llm-d-conformance-manifests"
WORKFLOW=".github/workflows/sync-main.yaml"

if [ -z "$1" ]; then
    echo "Usage: $0 <new-release-branch>"
    echo "Example: $0 3.5-ea1"
    echo ""
    echo "Current RELEASE_BRANCH:"
    grep "RELEASE_BRANCH:" "$REPO_DIR/$WORKFLOW" | head -1
    exit 1
fi

NEW_BRANCH="$1"
echo "Updating RELEASE_BRANCH to: $NEW_BRANCH"

# 1. Unlock main
echo "Unlocking main..."
gh api "repos/$REPO/branches/main/protection" -X PUT --silent --input - <<EOF
{"required_status_checks":null,"enforce_admins":false,"required_pull_request_reviews":null,"restrictions":null,"lock_branch":false}
EOF

# 2. Update the workflow file
cd "$REPO_DIR"
git checkout main
git pull origin main

sed -i "s/RELEASE_BRANCH: .*/RELEASE_BRANCH: $NEW_BRANCH/" "$WORKFLOW"
echo "Updated $WORKFLOW:"
grep "RELEASE_BRANCH:" "$WORKFLOW"

# 3. Commit and push
git add "$WORKFLOW"
git commit -m "Update sync source to $NEW_BRANCH"
git push origin main

# 4. Re-lock main
echo "Re-locking main..."
gh api "repos/$REPO/branches/main/protection" -X PUT --silent --input - <<EOF
{"required_status_checks":null,"enforce_admins":true,"required_pull_request_reviews":null,"restrictions":null,"lock_branch":true}
EOF

echo "Done. main will now sync from $NEW_BRANCH every 15 minutes."
