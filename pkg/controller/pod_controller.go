package controller

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	"net"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/keymutex"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/translator"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type podController struct {
	baseController

	recorder record.EventRecorder
	keyMutex keymutex.KeyMutex
}

func (r *podController) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.config.WorkerNum,
		}).
		Complete(r)
}

func (r *podController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
	)

	r.keyMutex.LockKey(req.String())
	defer func() { _ = r.keyMutex.UnlockKey(req.String()) }()

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("pod not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to get pod")
		return ctrl.Result{}, err
	}

	if pod.Spec.HostNetwork {
		logger.Info("pod is host network, not managed by kube-ovn")
		return ctrl.Result{}, nil
	}

	//if pod.Annotations != nil && pod.Annotations[consts.AnnotationRouted] == "true" {
	//	logger.Info("pod has already been routed, not need to reconcile")
	//	return ctrl.Result{}, nil
	//}

	if pod.DeletionTimestamp.IsZero() {
		if !utils.ContainsString(pod.Finalizers, consts.FinalizerController) {
			pod.Finalizers = append(pod.Finalizers, consts.FinalizerController)
			if err := r.Update(ctx, &pod); err != nil {
				logger.Error(err, "failed to add finalizer to pod")
				return ctrl.Result{}, err
			}
		} else {
			return r.handleAddOrUpdatePod(ctx, &pod)
		}
	} else {
		if utils.ContainsString(pod.Finalizers, consts.FinalizerController) {
			result, err := r.handleDeletePod(ctx, &pod)
			if err == nil {
				pod.Finalizers = utils.RemoveString(pod.Finalizers, consts.FinalizerController)
				if err := r.Update(ctx, &pod); err != nil {
					logger.Error(err, "failed to remove finalizer from pod")
					return ctrl.Result{}, err
				}
			}

			return result, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *podController) handleDeletePod(ctx context.Context, pod *corev1.Pod) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error

		podName = pod.Name
		podNs   = pod.Namespace
		podKey  = fmt.Sprintf("%s/%s", podNs, podName)
	)

	logger.Info("handle delete pod")

	deleteNetwork := true
	if ok, ssName, ssUID := utils.IsStatefulSetPod(pod); ok {
		deleteNetwork, err = r.isDeleteStatefulSetPodNetwork(ctx, pod, ssName, ssUID)
		if err != nil {
			logger.Error(err, "failed to check pod network for statefulSet")
			return ctrl.Result{}, err
		}
	}

	if deleteNetwork {
		if err = translator.Pod.UninstallPodNets(ctx, podKey); err != nil {
			logger.Error(err, "failed to uninstall pod nets")
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}

		r.ipam.ReleaseAddressByPod(ctx, podKey, "")

		var (
			nodeName = pod.Spec.NodeName
		)
		podNets, err := r.getPodOvnNets(ctx, pod)
		if err != nil {
			logger.Error(err, "failed to get pod ovn nets")
			return ctrl.Result{}, err
		}
		for _, podNet := range podNets {
			if err = r.DeleteIPCR(ctx, podName, podNs, "", nodeName, podNet.providerName); err != nil {
				logger.Error(err, "failed to delete ip cr")
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *podController) isDeleteStatefulSetPodNetwork(ctx context.Context, pod *corev1.Pod, ssName string, ssUID types.UID) (bool, error) {
	var (
		logger = log.FromContext(ctx).WithValues("statefulSet", ssName)
		err    error

		podNsName = pod.Namespace
	)

	var ss appsv1.StatefulSet
	if err = r.Get(ctx, types.NamespacedName{Name: ssName, Namespace: podNsName}, &ss); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("statefulSet has been deleted")
			return true, nil
		}

		logger.Error(err, "failed to get statefulSet")
		return false, err
	}

	// recreate with a same name
	if ss.UID != ssUID {
		logger.Info("statefulSet is a newly created one", "oldUID", ssUID, "newUID", ss.UID)
		return true, nil
	}

	// down scale
	{
		temp := strings.Split(pod.Name, "-")
		idxStr := temp[len(temp)-1]
		idx, err := strconv.ParseInt(idxStr, 10, 0)
		if err != nil {
			logger.Error(err, "failed to parse %s to int", idxStr)
			return false, err
		}
		if idx >= int64(*ss.Spec.Replicas) {
			logger.Info("statefulSet is down scale")
			return true, nil
		}
	}

	// subnet changed, or subnet cidr changed
	{
		var (
			podSubnet      = pod.Annotations[consts.AnnotationSubnet]
			ownerRefSubnet = ss.Spec.Template.ObjectMeta.Annotations[consts.AnnotationSubnet]
		)

		targetSubnet := ownerRefSubnet
		if ownerRefSubnet == "" {
			var podNs corev1.Namespace
			if err = r.Get(ctx, types.NamespacedName{Name: podNsName}, &podNs); err != nil {
				logger.Error(err, "failed to get pod namespace")
				return false, err
			}
			if podNs.Annotations == nil || podNs.Annotations[consts.AnnotationSubnet] == "" {
				logger.Info("no subnet allocated to namespace", "namespace", podNsName)
				return true, nil
			}
			targetSubnet = podNs.Annotations[consts.AnnotationSubnet]

		}

		if podSubnet != targetSubnet {
			logger.Info("pod subnet not match with target subnet", "podSubnet", podSubnet, "targetSubnet", targetSubnet)
			return true, nil
		}

		var subnet ovnv1.Subnet
		if err = r.Get(ctx, types.NamespacedName{Name: podSubnet}, &subnet); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("pod subnet has been deleted")
				return true, nil
			}
			logger.Error(err, "failed to get pod subnet")
			return false, err
		}
		podIPs := pod.Annotations[consts.AnnotationIPAddress]
		if !utils.CIDRContainIP(subnet.Spec.CIDRBlock, podIPs) {
			logger.Info("pod's ip is not in the range of subnet cidr", "ips", podIPs, "cidr", subnet.Spec.CIDRBlock)
			return true, nil
		}
	}

	return false, nil
}

func (r *podController) handleAddOrUpdatePod(ctx context.Context, pod *corev1.Pod) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	logger.Info("handle add or update pod")

	targetPodNets, err := r.getPodOvnNets(ctx, pod)
	if err != nil {
		logger.Error(err, "failed to get pod nets")
		return ctrl.Result{}, nil
	}

	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	// 1. validate pod allocated network, if exists
	if err = r.validatePodNetwork(ctx, pod); err != nil {
		logger.Error(err, "failed to validate pod network")
		r.recorder.Eventf(pod, corev1.EventTypeWarning, "ValidatePodNetworkFailed", err.Error())
		return ctrl.Result{}, err
	}

	// 2. reconcile pod ports
	{
		targetPortNames := make(map[string]int)
		for _, podNet := range targetPodNets {
			targetPortNames[translator.PodNameToPortName(pod.Name, pod.Namespace, podNet.providerName)] = 0
		}

		portsToDel, providerToDel, err := translator.Pod.ReconcilePodPorts(ctx, pod, targetPortNames)
		if err != nil {
			logger.Error(err, "failed to sync ports for pod")
			return ctrl.Result{}, nil
		}

		if len(portsToDel) > 0 {
			for portName, subnetName := range portsToDel {
				if subnet, ok := r.ipam.Subnets[subnetName]; ok {
					subnet.ReleaseAddressWithNicName(ctx, pod.Name, portName)
				}
			}
		}

		if len(providerToDel) > 0 {
			for _, providerName := range providerToDel {
				for annotationKey := range pod.Annotations {
					if strings.HasPrefix(annotationKey, providerName) {
						delete(pod.Annotations, annotationKey)
					}
				}
			}

			if err = r.Update(ctx, pod); err != nil {
				logger.Error(err, "failed to update pod")
				return ctrl.Result{}, err
			}
		}
	}

	// 3. reconcile pod network
	{
		if !isPodAlive(pod) {
			logger.Info("pod is not alive, terminating reconcile")
			return ctrl.Result{}, nil
		}

		if err = r.reconcilePodNetwork(ctx, pod, targetPodNets); err != nil {
			logger.Error(err, "failed to reconcile pod network")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *podController) reconcilePodNetwork(ctx context.Context, pod *corev1.Pod, targetPodNets []*ovnNet) error {
	var (
		logger       = log.FromContext(ctx)
		err          error
		podNamespace = pod.Namespace
		podName      = pod.Name
	)

	for _, podNet := range targetPodNets {
		// 1. allocate
		{
			if !isPodAllocated(pod, podNet.providerName) || isSubnetCidrChanged(pod, podNet) {
				v4IP, v6IP, mac, subnet, err := r.acquireAddress(ctx, pod, podNet)
				if err != nil {
					logger.Error(err, "failed to acquire address for pod", "subnet", podNet.subnet.Name)
					r.recorder.Eventf(pod, corev1.EventTypeWarning, "AcquireAddressFailed", err.Error())
					return err
				}

				logger.Info("succeed to acquire address for pod", "subnet", podNet.subnet.Name, "ipv4", v4IP, "ipv6", v6IP, "mac", mac)

				ips := utils.GetStringIP(v4IP, v6IP)
				if err = utils.ValidatePodCidr(podNet.subnet.Spec.CIDRBlock, ips); err != nil {
					logger.Error(err, "pod addresses not in subnet cidr",
						"ips", ips, "subnet", podNet.subnet.Name, "cidr", podNet.subnet.Spec.CIDRBlock)
					r.recorder.Eventf(pod, corev1.EventTypeWarning, "ValidatePodNetworkFailed", err.Error())
					return err
				}

				pod.Annotations[fmt.Sprintf(consts.AnnotationIPAddressTemplate, podNet.providerName)] = ips

				if mac == "" {
					delete(pod.Annotations, fmt.Sprintf(consts.AnnotationMacAddressTemplate, podNet.providerName))
				} else {
					pod.Annotations[fmt.Sprintf(consts.AnnotationMacAddressTemplate, podNet.providerName)] = mac
				}

				pod.Annotations[fmt.Sprintf(consts.AnnotationCidrTemplate, podNet.providerName)] = subnet.Spec.CIDRBlock
				pod.Annotations[fmt.Sprintf(consts.AnnotationGatewayTemplate, podNet.providerName)] = subnet.Spec.Gateway

				if isOvnSubnet(podNet.subnet) {
					pod.Annotations[fmt.Sprintf(consts.AnnotationSubnetTemplate, podNet.providerName)] = subnet.Name
					if pod.Annotations[fmt.Sprintf(consts.AnnotationPodNicTypeTemplate, podNet.providerName)] == "" {
						pod.Annotations[fmt.Sprintf(consts.AnnotationPodNicTypeTemplate, podNet.providerName)] = r.config.PodNicType
					}
				} else {
					delete(pod.Annotations, fmt.Sprintf(consts.AnnotationSubnetTemplate, podNet.providerName))
					delete(pod.Annotations, fmt.Sprintf(consts.AnnotationPodNicTypeTemplate, podNet.providerName))
				}

				pod.Annotations[fmt.Sprintf(consts.AnnotationAllocatedTemplate, podNet.providerName)] = "true"

				if podNet.providerType != providerTypeIpam {
					if (subnet.Spec.Vlan == "" || subnet.Spec.LogicalGateway || subnet.Spec.U2OInterconnection) && subnet.Spec.Vpc != "" {
						pod.Annotations[fmt.Sprintf(consts.AnnotationVpcTemplate, podNet.providerName)] = subnet.Spec.Vpc
					}

					podNetInfo := translator.PodNetInfo{
						PodName:      podName,
						PodNamespace: podNamespace,
						Subnet:       subnet.Name,
						IPs:          ips,
						Mac:          mac,
						Vpc:          subnet.Spec.Vpc,
						Provider:     podNet.providerName,
					}
					if err = translator.Pod.InstallPodNet(ctx, podNetInfo); err != nil {
						r.recorder.Eventf(pod, corev1.EventTypeWarning, "FailedToInstallPodNet", err.Error())
						logger.Error(err, "failed to install pod net", "net", podNetInfo)
						return err
					}
				}

				if err = r.AddOrUpdateIPCR(ctx, podName, utils.GetPodType(pod), podNamespace, ips, mac, subnet.Name, pod.Spec.NodeName, podNet.providerName); err != nil {
					logger.Error(err, "failed to add or update ip cr for pod")
					return err
				}
			}
		}

		// 2. route
		{
			if isOvnSubnet(podNet.subnet) &&
				pod.Spec.NodeName != "" &&
				!isPodRouted(pod, podNet.providerName) {
				podIP := pod.Annotations[fmt.Sprintf(consts.AnnotationIPAddressTemplate, podNet.providerName)]
				if podIP == "" {
					return fmt.Errorf("pod %s/%s has no IP address for net %s", pod.Name, pod.Namespace, podNet.providerName)
				}
				subnet := podNet.subnet
				if subnet == nil {
					return fmt.Errorf("pod %s/%s net %s has no subnet", pod.Name, pod.Namespace, podNet.providerName)
				}

				if subnet.Spec.GatewayType == consts.GatewayTypeDistributed {
					nodeJoinIP, err := r.getNodeJoinIP(ctx, pod.Spec.NodeName)
					if err != nil {
						logger.Error(err, "failed to get node join ip")
						return err
					}

					var added bool

					for _, joinIP := range nodeJoinIP {
						for _, pip := range strings.Split(podIP, ",") {
							if utils.CheckProtocol(joinIP.String()) != utils.CheckProtocol(pip) {
								continue
							}

							if err := translator.Pod.AddPodToSubnet(ctx, pod, subnet.Name, podNet.providerName); err != nil {
								logger.Error(err, "failed to add pod to subnet")
								return err
							}

							added = true
							break
						}

						if added {
							break
						}
					}
				}

				pod.Annotations[fmt.Sprintf(consts.AnnotationRoutedTemplate, podNet.providerName)] = "true"
			}
		}

		if err = r.Update(ctx, pod); err != nil {
			logger.Error(err, "failed to reconcile pod network", "podNet", podNet)
			return err
		}
	}

	return nil
}

func (r *podController) validatePodNetwork(_ context.Context, pod *corev1.Pod) error {
	var errors []error
	// cidr
	cidr := pod.Annotations[consts.AnnotationCidr]
	if cidr != "" {
		if err := utils.CheckCidrs(cidr); err != nil {
			errors = append(errors, err)
		}
	}

	// ip
	if ips := pod.Annotations[consts.AnnotationIPAddress]; ips != "" {
		for _, ip := range strings.Split(ips, ",") {
			if strings.Contains(ip, "/") {
				if _, _, err := net.ParseCIDR(ip); err != nil {
					errors = append(errors, fmt.Errorf("%s is not a valid ip", ip))
					continue
				}
			} else {
				if net.ParseIP(ip) == nil {
					errors = append(errors, fmt.Errorf("%s is not a valid ip", ip))
					continue
				}
			}

			if cidr != "" && !utils.CIDRContainIP(cidr, ip) {
				errors = append(errors, fmt.Errorf("%s not in cidr %s", ip, cidr))
				continue
			}
		}
	}

	// mac
	if mac := pod.Annotations[consts.AnnotationMacAddress]; mac != "" {
		if _, err := net.ParseMAC(mac); err != nil {
			errors = append(errors, fmt.Errorf("%s is not a valid mac", mac))
		}
	}

	return utilerrors.NewAggregate(errors)
}

func (r *podController) acquireAddress(ctx context.Context, pod *corev1.Pod, podNet *ovnNet) (string, string, string, *ovnv1.Subnet, error) {
	logger := log.FromContext(ctx)

	podName := pod.Name
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	var macStr *string
	if isOvnSubnet(podNet.subnet) {
		mac := pod.Annotations[fmt.Sprintf(consts.AnnotationMacAddressTemplate, podNet.providerName)]
		if mac != "" {
			if _, err := net.ParseMAC(mac); err != nil {
				return "", "", "", podNet.subnet, err
			}
			macStr = &mac
		}
	} else {
		macStr = new(string)
		*macStr = ""
	}

	portName := translator.PodNameToPortName(podName, pod.Namespace, podNet.providerName)
	ipStr := pod.Annotations[fmt.Sprintf(consts.AnnotationIPAddressTemplate, podNet.providerName)]
	var reallocate bool
	if ipStr != "" && !utils.CIDRContainIP(podNet.subnet.Spec.CIDRBlock, ipStr) {
		reallocate = true
	}
	if ipStr == "" || reallocate { // Random allocate
		var skippedIPs []string
		ipv4, ipv6, mac, err := r.ipam.GetRandomAddress(ctx, key, portName, macStr, podNet.subnet.Name, "", skippedIPs, true)
		if err != nil {
			logger.Error(err, "failed to acquire random address")
			return "", "", "", podNet.subnet, err
		}
		return ipv4, ipv6, mac, podNet.subnet, nil

	} else {
		ipv4, ipv6, mac, err := r.ipam.GetStaticAddress(ctx, key, portName, ipStr, macStr, podNet.subnet.Name, true)
		if err != nil {
			logger.Error(err, "failed to acquire static address")
			return "", "", "", podNet.subnet, err
		}
		return ipv4, ipv6, mac, podNet.subnet, nil
	}
}

func (r *podController) getNodeJoinIP(ctx context.Context, nodeName string) ([]net.IP, error) {
	node := &corev1.Node{}
	if err := r.Get(ctx, client.ObjectKey{Name: nodeName}, node); err != nil {
		return nil, fmt.Errorf("failed to get node %s: %v", nodeName, err)
	}

	joinIP := node.Annotations[consts.AnnotationJoinIP]
	if joinIP == "" {
		return nil, fmt.Errorf("failed to find join ip for node %s", nodeName)
	}

	var ips []net.IP
	for _, ip := range strings.Split(joinIP, ",") {
		ips = append(ips, net.ParseIP(ip))
	}

	return ips, nil
}

func isPodAllocated(pod *corev1.Pod, provider string) bool {
	if pod.Annotations == nil {
		return false
	}

	var allocated bool
	if provider == "" {
		allocated = pod.Annotations[consts.AnnotationAllocated] == "true"
	} else {
		allocated = pod.Annotations[fmt.Sprintf(consts.AnnotationAllocatedTemplate, provider)] == "true"
	}
	return allocated
}

func isSubnetCidrChanged(pod *corev1.Pod, podNet *ovnNet) bool {
	if len(pod.Annotations) == 0 || podNet.subnet == nil {
		return false
	}

	key := consts.AnnotationCidr
	if podNet.providerName != "" {
		key = fmt.Sprintf(consts.AnnotationCidrTemplate, podNet.providerName)
	}
	return pod.Annotations[key] != podNet.subnet.Spec.CIDRBlock
}

func isPodRouted(pod *corev1.Pod, provider string) bool {
	if pod.Annotations == nil {
		return false
	}

	var routed bool
	if provider == "" {
		routed = pod.Annotations[consts.AnnotationRouted] == "true"
	} else {
		routed = pod.Annotations[fmt.Sprintf(consts.AnnotationRoutedTemplate, provider)] == "true"
	}
	return routed
}

func (r *podController) podNeedSync(pod *corev1.Pod) (bool, error) {
	// 1. check annotations
	if pod.Annotations == nil {
		return true, nil
	}
	// 2. check annotation ovs subnet
	if pod.Annotations[consts.AnnotationRouted] != "true" {
		return true, nil
	}

	return false, nil
}

func isPodAlive(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil && pod.DeletionGracePeriodSeconds != nil {
		gracePeriod := time.Duration(*pod.DeletionGracePeriodSeconds) * time.Second
		if time.Now().After(pod.DeletionTimestamp.Add(gracePeriod)) {
			return false
		}
	}
	return true
}

func isPodStatusPhaseAlive(p *corev1.Pod) bool {
	if p.Status.Phase == corev1.PodSucceeded && p.Spec.RestartPolicy != corev1.RestartPolicyAlways {
		return false
	}

	if p.Status.Phase == corev1.PodFailed && p.Spec.RestartPolicy == corev1.RestartPolicyNever {
		return false
	}

	if p.Status.Phase == corev1.PodFailed && p.Status.Reason == "Evicted" {
		return false
	}
	return true
}
