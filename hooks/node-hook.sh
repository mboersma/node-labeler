#!/usr/bin/env bash

if [[ $1 == "--config" ]]; then
  cat <<EOF
{
  "onKubernetesEvent": [
    {
      "name": "OnNodeAdded",
      "kind": "node",
      "event": ["add"]
    },
    {
      "name": "OnNodeUpdated",
      "kind": "node",
      "event": ["update"],
      "jqFilter": ".metadata.labels"
    }
  ]
}
EOF
else
  set -euo pipefail

  MASTER_LABELS="kubernetes.azure.com/role=master kubernetes.io/role=master node-role.kubernetes.io/master="
  AGENT_LABELS="kubernetes.azure.com/role=agent kubernetes.io/role=agent node-role.kubernetes.io/agent="
  resourceName=$(jq -r '.[0].resourceName' "$BINDING_CONTEXT_PATH")
  currentLabels=$(kubectl label node "$resourceName" --list)

  # check the existing node labels to identify it as a master or agent
  labelsToApply=$AGENT_LABELS
  for label in $currentLabels; do
    if [[ $MASTER_LABELS == *$label* ]]; then
      labelsToApply=$MASTER_LABELS
      break
    fi
  done

  # check the existing node labels again to see if any are missing
  COUNTER=0
  for label in $currentLabels; do
    if [[ $labelsToApply == *$label* ]]; then
      COUNTER=$((COUNTER + 1))
    fi
  done

  if [[ $COUNTER -lt 3 ]]; then
    # apply all expected master or agent labels to the node
    results=$(kubectl label --overwrite node "$resourceName" $labelsToApply 2>&1)
    echo "$(date --utc +%FT%TZ) WARN     : $results"
  fi
fi
