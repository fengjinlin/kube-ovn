package controller

import (
	"context"
	"fmt"
	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/translator"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/keymutex"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"strings"
)

type nodeController struct {
	baseController

	recorder record.EventRecorder
	keyMutex keymutex.KeyMutex
}

func (r *nodeController) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(r)
}

func (r *nodeController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		logger   = log.FromContext(ctx)
		nodeName = req.Name
	)

	r.keyMutex.LockKey(nodeName)
	defer func() { _ = r.keyMutex.UnlockKey(nodeName) }()

	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("node not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to get node")
		return ctrl.Result{}, err
	}

	if node.DeletionTimestamp.IsZero() {
		if !utils.ContainsString(node.Finalizers, consts.FinalizerController) {
			node.Finalizers = append(node.Finalizers, consts.FinalizerController)
			if err := r.Update(ctx, &node); err != nil {
				logger.Error(err, "failed to add kube-ovn finalizer to node")
				return ctrl.Result{}, err
			}
		} else {
			return r.handleAddOrUpdateNode(ctx, &node)
		}
	} else {
		if utils.ContainsString(node.Finalizers, consts.FinalizerController) {
			result, err := r.handleDeleteNode(ctx, &node)
			if err == nil {
				node.Finalizers = utils.RemoveString(node.Finalizers, consts.FinalizerController)
				if err := r.Update(ctx, &node); err != nil {
					logger.Error(err, "failed to remove finalizer from subnet")
					return ctrl.Result{}, err
				}
			}

			return result, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *nodeController) handleAddOrUpdateNode(ctx context.Context, node *corev1.Node) (ctrl.Result, error) {
	var (
		logger   = log.FromContext(ctx)
		nodeName = node.Name

		err        error
		defaultVpc = r.config.DefaultVpc
		joinSubnet = r.config.JoinSubnet
		joinCIDR   = r.config.JoinSubnetCIDR

		portName           = fmt.Sprintf("node-%s", nodeName)
		v4NodeIP, v6NodeIP = utils.GetNodeInternalIP(node)
		joinIPs, joinMac   string
	)

	logger.Info("handle node add or update")

	// 1. allocate join network
	{
		if err = r.CheckIPsInSubnetCidr(ctx, defaultVpc, v4NodeIP, v6NodeIP); err != nil {
			r.recorder.Eventf(
				&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node.Name, UID: types.UID(node.Name)}},
				corev1.EventTypeWarning,
				"NodeAddressConflictWithSubnet",
				fmt.Sprintf("Internal IP address of node %s is in CIDR of subnet %s, this may result in network issues", node.Name, defaultVpc),
			)
			return ctrl.Result{}, err
		}

		var v4JoinIP, v6JoinIP string
		if node.Annotations[consts.AnnotationAllocated] == "true" &&
			node.Annotations[consts.AnnotationJoinIP] != "" &&
			node.Annotations[consts.AnnotationJoinMac] != "" {
			mac := node.Annotations[consts.AnnotationJoinMac]

			v4JoinIP, v6JoinIP, joinMac, err = r.ipam.GetStaticAddress(ctx, portName, portName,
				node.Annotations[consts.AnnotationJoinIP], &mac, joinSubnet, true)
			if err != nil {
				logger.Error(err, "failed to alloc static ip addresses for node")
				return ctrl.Result{}, err
			}
			joinIPs = utils.GetStringIP(v4JoinIP, v6JoinIP)

		} else {

			v4JoinIP, v6JoinIP, joinMac, err = r.ipam.GetRandomAddress(ctx, portName, portName, nil, joinSubnet,
				"", nil, true)
			if err != nil {
				logger.Error(err, "failed to alloc random ip addresses for node")
				return ctrl.Result{}, err
			}
			joinIPs = utils.GetStringIP(v4JoinIP, v6JoinIP)
			// save join network info
			var subnet = ovnv1.Subnet{}
			if err = r.Get(ctx, client.ObjectKey{Name: joinSubnet}, &subnet); err != nil {
				logger.Error(err, "failed to get join subnet")
				return ctrl.Result{}, err
			}
			if len(node.Annotations) == 0 {
				node.Annotations = make(map[string]string)
			}
			node.Annotations[consts.AnnotationJoinIP] = joinIPs
			node.Annotations[consts.AnnotationJoinCidr] = joinCIDR
			node.Annotations[consts.AnnotationJoinGateway] = subnet.Spec.Gateway
			node.Annotations[consts.AnnotationJoinMac] = joinMac
			node.Annotations[consts.AnnotationAllocated] = "true"
			node.Annotations[consts.AnnotationJoinLogicalSwitch] = joinSubnet
			node.Annotations[consts.AnnotationJoinLogicalSwitchPort] = portName
			if err := r.Update(ctx, node); err != nil {
				logger.Error(err, "failed to patch join subnet info")
				return ctrl.Result{}, err
			}

			logger.Info("succeed to save join network info")
		}

		if err = r.AddOrUpdateIPCR(ctx, "", consts.KindNode, "", joinIPs, joinMac, joinSubnet, nodeName, ""); err != nil {
			logger.Error(err, "failed to add ip cr for node")
			return ctrl.Result{}, err
		}
	}

	// 2. reconcile node join network
	{
		err = translator.Node.ReconcileNodeJoinNetwork(ctx, nodeName, v4NodeIP, v6NodeIP, defaultVpc, joinSubnet, joinIPs, joinMac)
		if err != nil {
			logger.Error(err, "failed to reconcile node network")
			return ctrl.Result{}, err
		}
	}

	// 3. add outbound route for pods in this node, only support distributed subnet
	{
		subnetList := &ovnv1.SubnetList{}
		if err = r.List(ctx, subnetList); err != nil {
			logger.Error(err, "failed to list subnets")
			return ctrl.Result{}, err
		}
		for _, subnet := range subnetList.Items {

			if subnet.Spec.Vpc != defaultVpc ||
				subnet.Name == joinSubnet ||
				(subnet.Spec.Vlan != "" && !subnet.Spec.LogicalGateway) ||
				subnet.Spec.GatewayType != consts.GatewayTypeDistributed {
				continue
			}

			err = translator.Subnet.ReconcileOutboundRouteForDistributedSubnetInDefaultVpc(ctx, &subnet, node)
			if err != nil {
				logger.Error(err, "failed to add node gw route for subnet", "subnet", subnet.Name, "gw")
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *nodeController) handleDeleteNode(ctx context.Context, node *corev1.Node) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error

		nodeName   = node.Name
		defaultVpc = r.config.DefaultVpc
	)

	//if !r.validateNodeDeletion(ctx, node) {
	//	return ctrl.Result{}, fmt.Errorf("failed to validate node deletion")
	//}

	logger.Info("handle delete node")

	portName := fmt.Sprintf("node-%s", nodeName)
	addresses := r.ipam.GetPodAddress(portName)
	var nextHops []string
	for _, addr := range addresses {
		if addr.IP == "" {
			continue
		}
		nextHops = append(nextHops, addr.IP)
	}

	if err := translator.Node.CleanupNodeJoinNetwork(ctx, nodeName, defaultVpc, nextHops); err != nil {
		logger.Error(err, "failed to cleanup node networks")
		return ctrl.Result{}, err
	}

	logger.Info("release node port", "portName", portName)
	r.ipam.ReleaseAddressByPod(ctx, portName, defaultVpc)

	// del outbound route
	if err = r.deleteOutboundRoutesInDefaultVpc(ctx, node); err != nil {
		logger.Error(err, "failed to delete node gw route")
		return ctrl.Result{}, err
	}

	if err = r.DeleteIPCR(ctx, "", "", r.config.JoinSubnet, nodeName, ""); err != nil {
		logger.Error(err, "failed to delete node ip cr")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *nodeController) validateNodeDeletion(ctx context.Context, node *corev1.Node) bool {
	var (
		logger     = log.FromContext(ctx)
		defaultVpc = r.config.DefaultVpc
	)
	subnetList := &ovnv1.SubnetList{}
	if err := r.List(ctx, subnetList); err != nil {
		logger.Error(err, "failed to list subnets")
		return false
	}
	for _, subnet := range subnetList.Items {
		if subnet.Spec.Vpc == defaultVpc &&
			subnet.Spec.GatewayType == consts.GatewayTypeCentralized {
			if utils.ContainsString(strings.Split(subnet.Spec.GatewayNode, ","), node.Name) {
				logger.Error(nil, "node is used by subnet for gateway node", "subnet", subnet.Name)
				return false
			}
		}
	}
	return true
}

func (r *nodeController) deleteOutboundRoutesInDefaultVpc(ctx context.Context, node *corev1.Node) error {
	var (
		logger     = log.FromContext(ctx)
		err        error
		defaultVpc = r.config.DefaultVpc
		nodeName   = node.Name
	)
	subnetList := &ovnv1.SubnetList{}
	if err = r.List(ctx, subnetList); err != nil {
		logger.Error(err, "failed to list subnets")
		return err
	}
	for _, subnet := range subnetList.Items {
		if subnet.Spec.Vpc != defaultVpc ||
			(subnet.Spec.Vlan != "" && !subnet.Spec.LogicalGateway) {
			continue
		}

		switch subnet.Spec.GatewayType {
		case consts.GatewayTypeDistributed:
			if err := translator.Subnet.DeleteOutboundRouteForDistributedSubnetInDefaultVpc(ctx, defaultVpc, subnet.Name, subnet.Spec.CIDRBlock, nodeName); err != nil {
				logger.Error(err, "failed to delete node gw route")
				return err
			}

		case consts.GatewayTypeCentralized:
			gatewayNodes := strings.Split(subnet.Spec.GatewayNode, ",")
			var newGatewayNodes []string
			for _, gwNode := range gatewayNodes {
				if gwNode != nodeName {
					newGatewayNodes = append(newGatewayNodes, gwNode)
				}
			}
			if len(newGatewayNodes) < len(gatewayNodes) {
				subnet.Spec.GatewayNode = strings.Join(newGatewayNodes, ",")
				if err = r.Update(ctx, &subnet); err != nil {
					logger.Error(err, "failed to update subnet gateway nodes", "old", gatewayNodes, "new", newGatewayNodes)
					return err
				}
			}

		}

	}
	return nil

}

func nodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (r *nodeController) CheckIPsInSubnetCidr(ctx context.Context, vpcName string, ips ...string) error {
	logger := log.FromContext(ctx)

	subnetList := &ovnv1.SubnetList{}
	if err := r.List(ctx, subnetList); err != nil {
		logger.Error(err, "failed to list subnets")
		return err
	}
	// node ip not in subnet cidr
	for _, subnet := range subnetList.Items {
		if subnet.Spec.Vpc != vpcName || subnet.Spec.Vlan != "" {
			continue
		}
		v4Cidr, v6Cidr := utils.SplitStringIP(subnet.Spec.CIDRBlock)

		for _, ip := range ips {
			if utils.CIDRContainIP(v4Cidr, ip) || utils.CIDRContainIP(v6Cidr, ip) {
				return fmt.Errorf("ip %s is in CIDR of subnet %s", ip, subnet.Name)
			}
		}
	}
	return nil
}
