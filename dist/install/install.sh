#!/usr/bin/env bash
set -euo pipefail

echo "[Step 1/6] Label kube-ovn-master node and label datapath type"

LABEL="node-role.kubernetes.io/control-plane"
count=$(kubectl get node -l$LABEL --no-headers | wc -l)
if [ "$count" -eq 0 ]; then
  echo "ERROR: No node with label $LABEL found"
  exit 1
fi
kubectl label node -l$LABEL kube-ovn/role=master --overwrite

printf "[Step 1/6] Done...\n"




echo "[Step 2/6] Install OVN components"

kubectl apply -f crd/
kubectl apply -f sa/

kubectl apply -f ovn-central.yaml
kubectl apply -f ovn-ovs.yaml

kubectl rollout status deployment/ovn-central -n kube-system --timeout 300s
kubectl rollout status daemonset/ovn-ovs -n kube-system --timeout 120s

printf "[Step 2/6] Done...\n"




echo "[Step 3/6] Install Kube-OVN"

kubectl apply -f kube-ovn-cni.yaml
kubectl apply -f kube-ovn-controller.yaml

kubectl rollout status deployment/kube-ovn-controller -n kube-system --timeout 300s
kubectl rollout status daemonset/kube-ovn-cni -n kube-system --timeout 300s

printf "[Step 3/6] Done...\n"



echo "[Step 4/6] Delete pod that not in host network mode"

for ns in $(kubectl get ns --no-headers -o custom-columns=NAME:.metadata.name); do
  for pod in $(kubectl get pod --no-headers -n "$ns" --field-selector spec.restartPolicy=Always -o custom-columns=NAME:.metadata.name,HOST:spec.hostNetwork | awk '{if ($2!="true") print $1}'); do
    kubectl delete pod "$pod" -n "$ns" --ignore-not-found --wait=false
  done
done

kubectl rollout status deployment/coredns -n kube-system --timeout 300s

printf "[Step 4/6] Done...\n"




echo "[Step 5/6] Delete pod that not in host network mode"

printf "[Step 5/6] Done...\n"




echo "[Step 6/6] Delete pod that not in host network mode"

kubectl cp kube-system/"$(kubectl  -n kube-system get pods -o wide | grep cni | awk '{print $1}' | awk 'NR==1{print}')":/kube-ovn/kubectl-ko /usr/local/bin/kubectl-ko
chmod +x /usr/local/bin/kubectl-ko
kubectl ko diagnose all

printf "[Step 6/6] Done...\n"



echo "Thanks for choosing Kube-OVN!"