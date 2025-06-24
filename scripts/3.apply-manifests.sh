#!/bin/bash

set -euo pipefail


kubectl apply -f ./manifests/ns.yaml
kubectl apply -f ./manifests/sa+rbac.yaml
kubectl apply -f ./manifests/svc.yaml
kubectl apply -f ./manifests/deploy.yaml
