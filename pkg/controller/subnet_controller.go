package controller

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/keymutex"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/translator"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type subnetController struct {
	baseController

	recorder record.EventRecorder
	keyMutex keymutex.KeyMutex

	updateStatusQueue workqueue.RateLimitingInterface
}

func (r *subnetController) SetupWithManager(mgr manager.Manager) error {
	ipMapFunc := func(ctx context.Context, object client.Object) []reconcile.Request {
		ipCr, ok := object.(*ovnv1.IP)
		if ok {
			r.updateStatusQueue.Add(ipCr.Spec.Subnet)
		}
		return nil
	}

	go func() {
		for r.processNextUpdateSubnetStatusWorkItem() {
		}
	}()

	return ctrl.NewControllerManagedBy(mgr).
		For(&ovnv1.Subnet{}).
		Watches(&ovnv1.IP{}, handler.EnqueueRequestsFromMapFunc(ipMapFunc)).
		Complete(r)
}

func (r *subnetController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		logger     = log.FromContext(ctx)
		subnetName = req.Name
	)

	r.keyMutex.LockKey(subnetName)
	defer func() { _ = r.keyMutex.UnlockKey(subnetName) }()

	var subnet ovnv1.Subnet
	if err := r.Get(ctx, req.NamespacedName, &subnet); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("subnet not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to get subnet")
		return ctrl.Result{}, err
	}

	if subnet.DeletionTimestamp.IsZero() {
		if !utils.ContainsString(subnet.Finalizers, consts.FinalizerController) {
			subnet.Finalizers = append(subnet.Finalizers, consts.FinalizerController)
			if err := r.Update(ctx, &subnet); err != nil {
				logger.Error(err, "failed to add finalizer to subnet")
				return ctrl.Result{}, err
			}
		} else {
			return r.handleAddOrUpdateSubnet(ctx, &subnet)
		}
	} else {
		if utils.ContainsString(subnet.Finalizers, consts.FinalizerDefaultProtection) {
			r.recorder.Eventf(&subnet, corev1.EventTypeWarning, "SubnetDefaultProtection",
				"subnet has been protected by kube-ovn, if you are sure to delete it, remove default-protection finalizer from subnet")
			return ctrl.Result{}, nil
		}

		if utils.ContainsString(subnet.Finalizers, consts.FinalizerController) {
			result, err := r.handleDeleteSubnet(ctx, &subnet)
			if err == nil {
				subnet.Finalizers = utils.RemoveString(subnet.Finalizers, consts.FinalizerController)
				if err := r.Update(ctx, &subnet); err != nil {
					logger.Error(err, "failed to remove finalizer from subnet")
					return ctrl.Result{}, err
				}
			}

			return result, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *subnetController) handleAddOrUpdateSubnet(ctx context.Context, subnet *ovnv1.Subnet) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error

		defaultVpc = r.config.DefaultVpc
	)

	logger.Info("handle add or update subnet")

	// subnet provider is not ovn, and vpc is empty, should not reconcile
	if subnet.Spec.Provider != "" && subnet.Spec.Provider != consts.OvnProviderName {
		logger.Info("non ovn subnet is ready")
		return r.updateSubnetStatusReady(ctx, subnet, true, ovnv1.SubnetReasonNonOvnSubnet, "")
	}

	if err = r.formatAndValidateSubnet(ctx, subnet); err != nil {
		logger.Error(err, "failed to format and validate subnet")
		return r.updateSubnetStatusReady(ctx, subnet, false, "FailedToValidateSubnet", err.Error())
	}

	var vpc *ovnv1.Vpc
	if vpc, err = r.validateVpcBySubnet(ctx, subnet); err != nil {
		logger.Error(err, "failed to validate vpc by subnet")
		return r.updateSubnetStatusReady(ctx, subnet, false, "FailedToValidateVpc", err.Error())
	}

	needRouter := subnet.Spec.Vlan == "" || subnet.Spec.LogicalGateway
	err = translator.Subnet.CreateSubnetSwitch(ctx, subnet.Name, vpc.Status.Router, subnet.Spec.CIDRBlock, subnet.Spec.Gateway, needRouter)
	if err != nil {
		logger.Error(err, "failed to create subnet switch")
		return r.updateSubnetStatusReady(ctx, subnet, false, "FailedToCreateSwitch", err.Error())
	}

	if err = r.ipam.AddOrUpdateSubnet(ctx, subnet.Name, subnet.Spec.CIDRBlock, subnet.Spec.Gateway, subnet.Spec.ExcludeIPs); err != nil {
		logger.Error(err, "failed to add subnet to ipam")
		return r.updateSubnetStatusReady(ctx, subnet, false, "FailedToAddIPAM", err.Error())
	}

	if subnet.Name != r.config.JoinSubnet {
		lbs := []string{
			vpc.Status.TCPLoadBalancer,
			vpc.Status.TCPSessionLoadBalancer,
			vpc.Status.UDPLoadBalancer,
			vpc.Status.UDPSessionLoadBalancer,
			vpc.Status.SctpLoadBalancer,
			vpc.Status.SctpSessionLoadBalancer,
		}
		if r.config.EnableLb && subnet.Spec.EnableLb != nil && *subnet.Spec.EnableLb {
			if err = translator.Subnet.EnableSubnetLoadBalancer(ctx, subnet.Name, true, lbs...); err != nil {
				logger.Error(err, "failed to enable subnet load balancer")
				return ctrl.Result{}, err
			}
		} else {
			if err = translator.Subnet.EnableSubnetLoadBalancer(ctx, subnet.Name, false, lbs...); err != nil {
				logger.Error(err, "failed to disable subnet load balancer")
				return ctrl.Result{}, err
			}
		}
	}

	if subnet.Spec.Vpc == defaultVpc {
		if err = r.reconcileSubnetRouteInDefaultVpc(ctx, subnet); err != nil {
			logger.Error(err, "failed to reconcile subnet route in default vpc")
			return ctrl.Result{}, err
		}
	} else {
		if err = r.reconcileSubnetRouteInCustomVpc(ctx, subnet); err != nil {
			logger.Error(err, "failed to reconcile subnet route on custom vpc")
			return ctrl.Result{}, err
		}
	}

	if err = translator.Subnet.ReconcileSubnetACL(ctx, subnet, r.config.JoinSubnetCIDR); err != nil {
		logger.Error(err, "failed to reconcile subnet acl")
		return ctrl.Result{}, err
	}

	return r.updateSubnetStatusReady(ctx, subnet, true, "SubnetReady", "Subnet is ready to work")
}

func (r *subnetController) reconcileSubnetRouteInDefaultVpc(ctx context.Context, subnet *ovnv1.Subnet) error {
	var (
		logger     = log.FromContext(ctx)
		err        error
		defaultVpc = r.config.DefaultVpc
		joinSubnet = r.config.JoinSubnet
	)

	var vpc ovnv1.Vpc
	if err = r.Get(ctx, client.ObjectKey{Name: defaultVpc}, &vpc); err != nil {
		logger.Error(err, "failed to get default vpc")
		return err
	}

	if subnet.Spec.Vlan != "" && !subnet.Spec.LogicalGateway { // underlay network, and no logical gateway

	} else {
		if err = translator.Subnet.ReconcileCommonPolicyRouteForSubnetInDefaultVpc(ctx, subnet); err != nil {
			logger.Error(err, "failed to reconcile common policy route")
			return err
		}

		// join subnet only need to add vpc gateway
		if subnet.Name == joinSubnet {
			if err = translator.Vpc.AddNodeGwStaticRoute(ctx, defaultVpc, subnet.Spec.Gateway, len(vpc.Spec.StaticRoutes) > 0); err != nil {
				logger.Error(err, "failed to add gateway route for default vpc")
				return err
			}
			return nil
		}

		switch subnet.Spec.GatewayType {
		case consts.GatewayTypeDistributed:
			var nodeList corev1.NodeList
			if err = r.List(ctx, &nodeList); err != nil {
				logger.Error(err, "failed to list nodes")
				return err
			}
			for _, node := range nodeList.Items {
				if node.Annotations[consts.AnnotationAllocated] != "true" {
					continue
				}

				if err = translator.Subnet.ReconcileOutboundRouteForDistributedSubnetInDefaultVpc(ctx, subnet, &node); err != nil {
					logger.Error(err, "failed to reconcile node gateway network for distributed subnet in default vpc")
					return err
				}
			}

		case consts.GatewayTypeCentralized:
			if subnet.Spec.GatewayNode == "" {
				logger.Info("Spec.GatewayNode field must be specified for centralized gateway type")
				return nil
			}

			// check gw node available
			var gatewayNodes []*corev1.Node
			for _, nodeName := range strings.Split(subnet.Spec.GatewayNode, ",") {
				var node corev1.Node
				if err = r.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
					if apierrors.IsNotFound(err) {
						logger.Info("node not exists", "node", nodeName)
						continue
					}
					logger.Error(err, "failed to get node", "node", nodeName)
					return err
				}
				if !nodeReady(&node) {
					logger.Info("gateway node is not ready", "node", nodeName)
					continue
				}
				if node.Annotations == nil ||
					node.Annotations[consts.AnnotationAllocated] != "true" ||
					node.Annotations[consts.AnnotationJoinIP] == "" {
					logger.Info("gateway node is not allocated", "node", nodeName)
					continue
				}
				gatewayNodes = append(gatewayNodes, &node)
			}
			if len(gatewayNodes) == 0 {
				return fmt.Errorf("no available gateway nodes")
			}

			var (
				activateGwNode                 = subnet.Status.ActivateGateway
				gatewayNodeNames               []string
				v4ActivateGwIP, v6ActivateGwIP string
				v4GwIPs, v6GwIPs               []string
				v4NodeNameIPMap                = make(map[string]string, len(gatewayNodes))
				v6NodeNameIPMap                = make(map[string]string, len(gatewayNodes))
			)
			for _, node := range gatewayNodes {
				nodeName := node.Name

				joinIPs := node.Annotations[consts.AnnotationJoinIP]
				v4JoinIP, v6JoinIP := utils.SplitStringIP(joinIPs)
				if v4JoinIP != "" {
					v4GwIPs = append(v4GwIPs, v4JoinIP)
					v4NodeNameIPMap[nodeName] = v4JoinIP
				}
				if v6JoinIP != "" {
					v6GwIPs = append(v6GwIPs, v6JoinIP)
					v6NodeNameIPMap[nodeName] = v6JoinIP
				}
				if nodeName == activateGwNode {
					v4ActivateGwIP = v4JoinIP
					v6ActivateGwIP = v6JoinIP
				}
				gatewayNodeNames = append(gatewayNodeNames, nodeName)
			}

			var (
				v4CIDR, v6CIDR         = utils.SplitStringIP(subnet.Spec.CIDRBlock)
				v4Nexthops, v6Nexthops []string

				v4OK = len(v4GwIPs) > 0 && v4CIDR != ""
				v6OK = len(v6GwIPs) > 0 && v6CIDR != ""
			)
			if subnet.Spec.EnableEcmp {
				if v4OK {
					v4Nexthops = v4GwIPs
				}
				if v6OK {
					v6Nexthops = v6GwIPs
				}
			} else {
				if v4ActivateGwIP == "" && v6ActivateGwIP == "" { // invalid activate gw node, reselect one
					activateGwNode = gatewayNodeNames[rand.Intn(len(gatewayNodeNames))]
				}

				v4NodeNameIPMap = map[string]string{
					activateGwNode: v4NodeNameIPMap[activateGwNode],
				}
				v6NodeNameIPMap = map[string]string{
					activateGwNode: v6NodeNameIPMap[activateGwNode],
				}

				if v4OK {
					if v4ActivateGwIP != "" {
						v4Nexthops = []string{v4ActivateGwIP}
					} else {
						if ip, ok := v4NodeNameIPMap[activateGwNode]; ok {
							v4Nexthops = []string{ip}
						}
					}
				}
				if v6OK {
					if v6ActivateGwIP != "" {
						v6Nexthops = []string{v6ActivateGwIP}
					} else {
						if ip, ok := v6NodeNameIPMap[activateGwNode]; ok {
							v6Nexthops = []string{ip}
						}
					}
				}
			}

			if activateGwNode != subnet.Status.ActivateGateway {
				logger.V(4).Info("subnet activate gateway changed", "old", subnet.Status.ActivateGateway, "new", activateGwNode)

				subnet.Status.ActivateGateway = activateGwNode
				if err = r.Status().Update(ctx, subnet); err != nil {
					logger.Error(err, "failed to update subnet activate gateway")
					return err
				}
			}

			if err = translator.Subnet.ReconcileOutboundRouteForCentralizedSubnetInDefaultVpc(ctx, subnet, v4Nexthops, v6Nexthops, v4NodeNameIPMap, v6NodeNameIPMap); err != nil {
				logger.Error(err, "failed to reconcile node gateway network for distributed subnet in default vpc",
					"v4Nexthops", v4Nexthops, "v6Nexthops", v6Nexthops)
				return err
			}
		}
	}

	return nil
}

func (r *subnetController) reconcileSubnetRouteInCustomVpc(ctx context.Context, subnet *ovnv1.Subnet) error {
	//var (
	//	logger  = log.FromContext(ctx)
	//	err     error
	//)

	return nil
}

func (r *subnetController) updateSubnetStatusReady(ctx context.Context, subnet *ovnv1.Subnet, ready bool, reason, msg string) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)
	if ready {
		subnet.Status.Ready(reason, msg)
		r.recorder.Eventf(subnet, corev1.EventTypeNormal, reason, msg)
	} else {
		subnet.Status.NotReady(reason, msg)
		r.recorder.Eventf(subnet, corev1.EventTypeWarning, reason, msg)
	}
	if err = r.Status().Update(ctx, subnet); err != nil {
		logger.Error(err, "failed to update subnet status ready")
		return ctrl.Result{}, err
	}

	if !ready {
		err = errors.New(reason)
	}

	return ctrl.Result{}, err
}

func (r *subnetController) handleDeleteSubnet(ctx context.Context, subnet *ovnv1.Subnet) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	// 1. cleanup subnet common policy route
	if err = translator.Subnet.CleanupSubnetCommonPolicyRoute(ctx, subnet); err != nil {
		logger.Error(err, "failed to cleanup subnet common policy route")
		return ctrl.Result{}, err
	}

	// 2. cleanup subnet outbound policy route
	if subnet.Name != r.config.JoinSubnet {
		var nodeList corev1.NodeList
		if err = r.List(ctx, &nodeList); err != nil {
			logger.Error(err, "failed to list nodes")
			return ctrl.Result{}, err
		}
		if err = translator.Subnet.CleanupSubnetOutboundPolicyRoute(ctx, subnet, &nodeList); err != nil {
			logger.Error(err, "failed to cleanup subnet outbound policy route")
			return ctrl.Result{}, err
		}
	}

	// 3. cleanup subnet switch
	{
		var router string
		var vpc ovnv1.Vpc
		if err = r.Get(ctx, types.NamespacedName{Name: subnet.Spec.Vpc}, &vpc); err != nil {
			if !apierrors.IsNotFound(err) {
				logger.Error(err, "failed to get vpc", "vpc", subnet.Spec.Vpc)
				return ctrl.Result{}, err
			}
			router = r.config.DefaultVpc
		} else {
			router = vpc.Status.Router
		}

		if err = translator.Subnet.DeleteSubnetSwitch(ctx, subnet, router); err != nil {
			logger.Error(err, "failed to delete subnet switch")
			return ctrl.Result{}, err
		}
	}

	// 4. update namespaces which subnet bind to
	{
		var nsList corev1.NamespaceList
		if err = r.List(ctx, &nsList); err != nil {
			logger.Error(err, "failed to list namespaces")
			return ctrl.Result{}, err
		}
		for _, ns := range nsList.Items {
			if ns.Annotations == nil {
				continue
			}
			if ns.Annotations[consts.AnnotationSubnet] == subnet.Name {
				delete(ns.Annotations, consts.AnnotationSubnet)
				if err = r.Update(ctx, &ns); err != nil {
					logger.Error(err, "failed to update namespace annotation for subnet deletion", "namespace", ns.Name)
					return ctrl.Result{}, err
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *subnetController) processNextUpdateSubnetStatusWorkItem() bool {
	obj, shutdown := r.updateStatusQueue.Get()
	if shutdown {
		return false
	}

	if err := func(obj interface{}) error {
		defer r.updateStatusQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			r.updateStatusQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := r.handleUpdateSubnetStatus(key); err != nil {
			r.updateStatusQueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		r.updateStatusQueue.Forget(obj)
		return nil
	}(obj); err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (r *subnetController) handleUpdateSubnetStatus(key string) error {
	r.keyMutex.LockKey(key)
	defer func() { _ = r.keyMutex.UnlockKey(key) }()

	var (
		ctx, cancel = context.WithCancel(context.TODO())
		logger      = log.FromContext(ctx).WithValues("subnet", key, "reconcileID", uuid.New().String())
		err         error
	)
	defer cancel()

	logger.Info("handle update subnet status")

	var subnet ovnv1.Subnet
	if err = r.Get(ctx, types.NamespacedName{Name: key}, &subnet); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("subnet not found")
			return nil
		}
		logger.Error(err, "failed to get subnet")
		return err
	}

	var usingIPList ovnv1.IPList
	if err = r.List(ctx, &usingIPList, client.MatchingLabels{consts.LabelSubnetName: key}); err != nil {
		logger.Error(err, "failed to list used ips")
		return err
	}
	numUsingIPs := float64(len(usingIPList.Items))
	var v4UsingIPs, v6UsingIPs float64
	switch subnet.Spec.Protocol {
	case utils.ProtocolDual:
		v4UsingIPs = numUsingIPs
		v6UsingIPs = numUsingIPs
	case utils.ProtocolIPv4:
		v4UsingIPs = numUsingIPs
	case utils.ProtocolIPv6:
		v6UsingIPs = numUsingIPs
	}
	// ip is allocated from subnet.spec.excludeIPs, do not count it as numUsedIPs
	for _, usingIP := range usingIPList.Items {
		for _, excludeIPs := range subnet.Spec.ExcludeIPs {
			if utils.ContainsIPs(excludeIPs, usingIP.Spec.V4IPAddress) {
				v4UsingIPs--
			}
			if utils.ContainsIPs(excludeIPs, usingIP.Spec.V6IPAddress) {
				v6UsingIPs--
			}
		}
	}

	var (
		v4ExcludeIPs, v6ExcludeIPs     = utils.SplitIpsByProtocol(subnet.Spec.ExcludeIPs)
		v4CidrStr, v6CidrStr           string
		v4AvailableIPs, v6AvailableIPs float64
	)

	for _, cidr := range strings.Split(subnet.Spec.CIDRBlock, ",") {
		switch utils.CheckProtocol(cidr) {
		case utils.ProtocolIPv4:
			v4CidrStr = cidr
			v4ExcludeIPs = utils.ExpandExcludeIPs(v4ExcludeIPs, cidr)
		case utils.ProtocolIPv6:
			v6CidrStr = cidr
			v6ExcludeIPs = utils.ExpandExcludeIPs(v6ExcludeIPs, cidr)
		}
	}

	if v4CidrStr != "" {
		_, v4Cidr, _ := net.ParseCIDR(v4CidrStr)
		v4AvailableIPs = utils.AddressCount(v4Cidr) - utils.CountIPNums(v4ExcludeIPs)
	}
	if v6CidrStr != "" {
		_, v6Cidr, _ := net.ParseCIDR(v6CidrStr)
		v6AvailableIPs = utils.AddressCount(v6Cidr) - utils.CountIPNums(v6ExcludeIPs)
	}

	v4AvailableIPs -= v4UsingIPs
	if v4AvailableIPs < 0 {
		v4AvailableIPs = 0
	}
	v6AvailableIPs -= v6UsingIPs
	if v6AvailableIPs < 0 {
		v6AvailableIPs = 0
	}

	v4UsingIPRange, v6UsingIPRange, v4AvailableIPRange, v6AvailableIPRange := r.ipam.GetSubnetIPRangeString(subnet.Name, subnet.Spec.ExcludeIPs)

	if subnet.Status.V4UsingIPs == v4UsingIPs &&
		subnet.Status.V6UsingIPs == v6UsingIPs &&
		subnet.Status.V4AvailableIPs == v4AvailableIPs &&
		subnet.Status.V6AvailableIPs == v6AvailableIPs &&
		subnet.Status.V4UsingIPRange == v4UsingIPRange &&
		subnet.Status.V6UsingIPRange == v6UsingIPRange &&
		subnet.Status.V4AvailableIPRange == v4AvailableIPRange &&
		subnet.Status.V6AvailableIPRange == v6AvailableIPRange {
		return nil
	} else {
		subnet.Status.V4UsingIPs = v4UsingIPs
		subnet.Status.V6UsingIPs = v6UsingIPs
		subnet.Status.V4AvailableIPs = v4AvailableIPs
		subnet.Status.V6AvailableIPs = v6AvailableIPs
		subnet.Status.V4UsingIPRange = v4UsingIPRange
		subnet.Status.V6UsingIPRange = v6UsingIPRange
		subnet.Status.V4AvailableIPRange = v4AvailableIPRange
		subnet.Status.V6AvailableIPRange = v6AvailableIPRange

		if err = r.Status().Update(ctx, &subnet); err != nil {
			logger.Error(err, "failed to update subnet status")
			return err
		}
	}

	return nil
}

func isOvnSubnet(subnet *ovnv1.Subnet) bool {
	return subnet.Spec.Provider == "" || subnet.Spec.Provider == consts.OvnProviderName || strings.HasPrefix(subnet.Spec.Provider, "ovn")
}
