#!/usr/bin/env bash
# Prints a kubeconfig whose users are the read-only ServiceAccounts from
# rbac.yaml, so kboba can be tried with real read-only permissions.
#
# Usage: readonly-kubeconfig.sh <admin-kubeconfig> <context>
#
# Only ever targets the kind cluster given explicitly via the admin
# kubeconfig/context. Note that `kubectl create token` (a TokenRequest) is
# performed here with admin credentials, by this script — not by kboba.
set -euo pipefail

admin_kubeconfig=$1
context=$2
kc=(kubectl --kubeconfig "$admin_kubeconfig" --context "$context")

server=$("${kc[@]}" config view --minify --raw -o jsonpath='{.clusters[0].cluster.server}')
ca=$("${kc[@]}" config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')
readonly_token=$("${kc[@]}" -n kboba-demo create token kboba-readonly --duration=24h)
limited_token=$("${kc[@]}" -n kboba-demo create token kboba-limited --duration=24h)

cat <<KUBECONFIG
apiVersion: v1
kind: Config
current-context: kboba-readonly
clusters:
  - name: kind-kboba
    cluster:
      server: ${server}
      certificate-authority-data: ${ca}
  - name: unreachable
    cluster:
      server: https://127.0.0.1:1
contexts:
  - name: kboba-readonly
    context:
      cluster: kind-kboba
      user: kboba-readonly
      namespace: kboba-demo
  - name: kboba-limited
    context:
      cluster: kind-kboba
      user: kboba-limited
      namespace: kboba-demo
  - name: kboba-unreachable
    context:
      cluster: unreachable
      user: kboba-readonly
users:
  - name: kboba-readonly
    user:
      token: ${readonly_token}
  - name: kboba-limited
    user:
      token: ${limited_token}
KUBECONFIG
