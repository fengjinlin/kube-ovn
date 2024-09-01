---
kind: Deployment
apiVersion: apps/v1
metadata:
  name: kube-ovn-controller
  namespace: kube-system
  annotations:
    kubernetes.io/description: |
      kube-ovn controller
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kube-ovn-controller
  strategy:
    rollingUpdate:
      maxSurge: 0%
      maxUnavailable: 100%
    type: RollingUpdate
  template:
    metadata:
      labels:
        app: kube-ovn-controller
        component: network
        type: infra
    spec:
      tolerations:
        - effect: NoSchedule
          operator: Exists
        - key: CriticalAddonsOnly
          operator: Exists
      affinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - preference:
                matchExpressions:
                  - key: "ovn.kubernetes.io/ic-gw"
                    operator: NotIn
                    values:
                      - "true"
              weight: 100
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            - labelSelector:
                matchLabels:
                  app: kube-ovn-controller
              topologyKey: kubernetes.io/hostname
      priorityClassName: system-cluster-critical
      serviceAccountName: ovn
      hostNetwork: true
      containers:
        - name: kube-ovn-controller
          image: {REGISTRY}/kube-ovn:{RELEASE_TAG}
          imagePullPolicy: Always
          args:
            - /kube-ovn/start-controller.sh
            - --default-subnet-cidr=10.244.0.0/16
            - --default-subnet-gateway=10.244.0.1
            - --default-subnet-gateway-check=true
            - --default-subnet-logical-gateway=false
            - --default-subnet-u2o-interconnection=false
            - --default-subnet-exclude-ips=
            - --join-subnet-cidr=100.64.0.0/16
            - --service-cluster-ip-range=10.96.0.0/12
            - --network-type=geneve
            - --default-interface-name=
            - --default-exchange-link-name=false
            - --default-vlan-id=100
            - --ls-dnat-mod-dl-dst=true
            - --ls-ct-skip-dst-lport-ips=true
            - --pod-nic-type=veth-pair
            - --enable-lb=true
            - --enable-np=true
            - --enable-eip-snat=true
            - --enable-external-vpc=true
            - --gc-interval=360
            - --inspect-interval=20
            - --enable-lb-svc=false
            - --keep-vm-ip=true
            - --node-local-dns-ip=
            - --log-file=/var/log/kube-ovn/kube-ovn-controller.log
          env:
            - name: ENABLE_SSL
              value: "false"
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: KUBE_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
            - name: KUBE_NODE_NAME
              valueFrom:
                fieldRef:
                  fieldPath: spec.nodeName
            - name: OVN_DB_IPS
              value: 10.1.1.201
            - name: POD_IPS
              valueFrom:
                fieldRef:
                  fieldPath: status.podIPs
            - name: ENABLE_BIND_LOCAL_IP
              value: "true"
          volumeMounts:
            - mountPath: /etc/localtime
              name: localtime
              readOnly: true
            - mountPath: /var/log/kube-ovn
              name: kube-ovn-log
            # ovn-ic log directory
            - mountPath: /var/log/ovn
              name: ovn-log
            - mountPath: /var/run/tls
              name: kube-ovn-tls
          readinessProbe:
            exec:
              command:
                - /kube-ovn/kube-ovn-controller-healthcheck
            periodSeconds: 3
            timeoutSeconds: 45
          livenessProbe:
            exec:
              command:
                - /kube-ovn/kube-ovn-controller-healthcheck
            initialDelaySeconds: 300
            periodSeconds: 7
            failureThreshold: 5
            timeoutSeconds: 45
          resources:
            requests:
              cpu: 200m
              memory: 200Mi
            limits:
              cpu: 1000m
              memory: 1Gi
      nodeSelector:
        kubernetes.io/os: "linux"
        kube-ovn/role: master
      volumes:
        - name: localtime
          hostPath:
            path: /etc/localtime
        - name: kube-ovn-log
          hostPath:
            path: /var/log/kube-ovn
        - name: ovn-log
          hostPath:
            path: /var/log/ovn
        - name: kube-ovn-tls
          secret:
            optional: true
            secretName: kube-ovn-tls

---
kind: Service
apiVersion: v1
metadata:
  name: kube-ovn-controller
  namespace: kube-system
  labels:
    app: kube-ovn-controller
spec:
  ipFamilyPolicy: PreferDualStack
  selector:
    app: kube-ovn-controller
  ports:
    - port: 10660
      name: metrics
