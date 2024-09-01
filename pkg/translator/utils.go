package translator

import (
	"fmt"
	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"strings"
)

func PodNameToPortName(pod, namespace, provider string) string {
	if provider == "" || provider == consts.OvnProviderName {
		return fmt.Sprintf("%s.%s", pod, namespace)
	}
	return fmt.Sprintf("%s.%s.%s", pod, namespace, provider)
}

func NodeNameToPortName(nodeName string) string {
	return fmt.Sprintf("node-%s", nodeName)
}

func OverlaySubnetsPortGroupName(subnetName, nodeName string) string {
	return strings.ReplaceAll(fmt.Sprintf("%s.%s", subnetName, nodeName), "-", ".")
}

func convertPolicy(origin ovnv1.RoutePolicy) *ovnnb.LogicalRouterStaticRoutePolicy {
	if origin == ovnv1.PolicyDst {
		return &ovnnb.LogicalRouterStaticRoutePolicyDstIP
	}
	return &ovnnb.LogicalRouterStaticRoutePolicySrcIP
}

func convertPolicyAction(origin ovnv1.PolicyRouteAction) ovnnb.LogicalRouterPolicyAction {
	switch origin {
	case ovnv1.PolicyRouteActionAllow:
		return ovnnb.LogicalRouterPolicyActionAllow
	case ovnv1.PolicyRouteActionReroute:
		return ovnnb.LogicalRouterPolicyActionReroute
	case ovnv1.PolicyRouteActionDrop:
		return ovnnb.LogicalRouterPolicyActionDrop
	}

	return ovnnb.LogicalRouterPolicyActionAllow
}
