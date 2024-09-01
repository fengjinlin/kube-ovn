package translator

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type vpcTranslator struct {
	translator
}

func (t *vpcTranslator) CreateVpcRouter(ctx context.Context, vpc *ovnv1.Vpc) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)
	if err = t.ovnNbClient.CreateLogicalRouter(ctx, vpc.Name); err != nil {
		logger.Error(err, "failed to create vpc router")
		return err
	}

	return nil
}

func (t *vpcTranslator) ReconcileVpcStaticRoutes(ctx context.Context, vpc *ovnv1.Vpc, gatewayIPs string) error {
	var (
		logger = log.FromContext(ctx)
	)

	staticRouteTablesSet := make(map[string]int)
	for _, route := range vpc.Spec.StaticRoutes {
		staticRouteTablesSet[route.RouteTable] = 0
	}

	staticRouteTarget := vpc.Spec.StaticRoutes
	if gatewayIPs != "" {
		if _, exists := staticRouteTablesSet[consts.RouteTableMain]; !exists {
			staticRouteTablesSet[consts.RouteTableMain] = 0
		}

		v4GatewayIP, v6GatewayIP := utils.SplitStringIP(gatewayIPs)
		if v4GatewayIP != "" {
			for table, _ := range staticRouteTablesSet {
				staticRouteTarget = append(staticRouteTarget,
					&ovnv1.StaticRoute{
						Policy:     ovnv1.PolicyDst,
						CIDR:       "0.0.0.0/0",
						NextHopIP:  v4GatewayIP,
						RouteTable: table,
					},
				)
			}
		}
		if v6GatewayIP != "" {
			for table, _ := range staticRouteTablesSet {
				staticRouteTarget = append(staticRouteTarget,
					&ovnv1.StaticRoute{
						Policy:     ovnv1.PolicyDst,
						CIDR:       "0.0.0.0/0",
						NextHopIP:  v6GatewayIP,
						RouteTable: table,
					},
				)
			}
		}
	}

	staticRouteExists, err := t.ovnNbClient.ListLogicalRouterStaticRoutes(ctx, vpc.Name, nil)
	if err != nil {
		logger.Error(err, "failed to list vpc static routes")
		return err
	}

	routeToDel, routeToAdd := mergeStaticRoute(staticRouteExists, staticRouteTarget)
	for _, route := range routeToDel {
		logger.Info("delete vpc static route", "route", route)
		if err = t.ovnNbClient.DeleteLogicalRouterStaticRouteByUUID(ctx, vpc.Name, route.UUID); err != nil {
			logger.Error(err, "failed to delete vpc static route", vpc.Name, "route", route)
			return err
		}
	}
	for _, route := range routeToAdd {
		logger.Info("add vpc static route", "route", route)
		var bfdId *string
		if route.BfdID != "" {
			bfdId = &route.BfdID
		}
		rt := &ovnnb.LogicalRouterStaticRoute{
			RouteTable: route.RouteTable,
			Policy:     convertPolicy(route.Policy),
			IPPrefix:   route.CIDR,
			BFD:        bfdId,
			Nexthop:    route.NextHopIP,
		}
		if err = t.ovnNbClient.AddLogicalRouterStaticRoute(ctx, vpc.Name, rt); err != nil {
			logger.Error(err, "failed to add vpc static route", "route", route)
			return err
		}
	}

	return nil
}

func mergeStaticRoute(exists []*ovnnb.LogicalRouterStaticRoute, target []*ovnv1.StaticRoute) (routeToDel []*ovnnb.LogicalRouterStaticRoute, routeToAdd []*ovnv1.StaticRoute) {
	existsMap := make(map[string]*ovnnb.LogicalRouterStaticRoute)
	for _, route := range exists {
		key := genStaticRouteItemKey(route.RouteTable, *route.Policy, route.IPPrefix, route.Nexthop)
		existsMap[key] = route
	}

	for _, route := range target {
		key := genStaticRouteItemKey(route.RouteTable, string(route.Policy), route.CIDR, route.NextHopIP)
		if _, ok := existsMap[key]; ok {
			delete(existsMap, key)
		} else {
			routeToAdd = append(routeToAdd, route)
		}
	}

	for _, route := range existsMap {
		routeToDel = append(routeToDel, route)
	}

	return
}

func genStaticRouteItemKey(table, policy, cidr, nextHop string) string {
	direction := "dst"
	if strings.Contains(policy, "src") || strings.Contains(policy, "Src") {
		direction = "src"
	}
	return fmt.Sprintf("%s:%s:%s=>%s", table, direction, cidr, nextHop)
}

func (t *vpcTranslator) AddNodeGwStaticRoute(ctx context.Context, vpcName, gw string, userCustom bool) error {
	logger := log.FromContext(ctx)

	if userCustom {
		existsRoutes, err := t.ovnNbClient.ListLogicalRouterStaticRoutes(ctx, vpcName, nil)
		if err != nil {
			logger.Error(err, "failed to list vpc static route", "vpc", vpcName)
			return err
		}
		if len(existsRoutes) != 0 {
			logger.Info("vpc static route existing, skip add static route for node gw")
			return nil
		}
	}

	for _, cidr := range []string{"0.0.0.0/0", "::/0"} {
		for _, nextHop := range strings.Split(gw, ",") {
			if utils.CheckProtocol(nextHop) != utils.CheckProtocol(cidr) {
				continue
			}

			route := &ovnnb.LogicalRouterStaticRoute{
				RouteTable: "",
				Policy:     &ovnnb.LogicalRouterStaticRoutePolicyDstIP,
				IPPrefix:   cidr,
				Nexthop:    nextHop,
			}

			logger.Info("adding vpc gw route", "vpc", vpcName, "gw", gw)
			err := t.ovnNbClient.AddLogicalRouterStaticRoute(ctx, vpcName, route)
			if err != nil {
				logger.Error(err, "failed to vpc gw route")
				return err
			}
		}
	}

	return nil
}

func (t *vpcTranslator) ReconcileVpcPolicyRoutes(ctx context.Context, vpc *ovnv1.Vpc, isDefaultVpc bool) error {
	var (
		logger                                 = log.FromContext(ctx)
		err                                    error
		policyRouteNeedDel, policyRouteNeedAdd []*ovnv1.PolicyRoute
	)

	if isDefaultVpc {
		var policyRouteExisted []*ovnv1.PolicyRoute
		lastPolicies := vpc.Annotations[consts.AnnotationVpcLastPolicies]
		if lastPolicies != "" {
			if err = json.Unmarshal([]byte(lastPolicies), &policyRouteExisted); err != nil {
				logger.Error(err, "failed to deserialize last policy routes")
				return err
			}
		}
		policyRouteNeedDel, policyRouteNeedAdd = diffPolicyRouteWithExisted(policyRouteExisted, vpc.Spec.PolicyRoutes)
	} else {
		var policyRouteExisted []*ovnnb.LogicalRouterPolicy
		if policyRouteExisted, err = t.ovnNbClient.ListLogicalRouterPolicy(ctx, vpc.Name, func(policy *ovnnb.LogicalRouterPolicy) bool {
			return true
		}); err != nil {
			logger.Error(err, "failed to list vpc policy routes")
			return err
		}
		policyRouteNeedDel, policyRouteNeedAdd = diffPolicyRouteWithExistedLogical(policyRouteExisted, vpc.Spec.PolicyRoutes)
	}

	for _, item := range policyRouteNeedDel {
		logger.Info("delete vpc logical router policy", "policy", item)
		filter := func(policy *ovnnb.LogicalRouterPolicy) bool {
			return policy.Priority == item.Priority && policy.Match == item.Match
		}
		if err = t.ovnNbClient.DeleteLogicalRouterPolicyByFilter(ctx, vpc.Name, filter); err != nil {
			logger.Error(err, "failed to delete vpc logical route policy", "policy", item)
			return err
		}
	}
	externalIDs := map[string]string{
		consts.ExternalIDsKeyVendor:        consts.CniVendorName,
		consts.ExternalIDsKeyLogicalRouter: vpc.Name,
	}
	for _, item := range policyRouteNeedAdd {
		logger.Info("add vpc logical router policy", "policy", item)
		policy := &ovnnb.LogicalRouterPolicy{
			Priority:    item.Priority,
			Match:       item.Match,
			Action:      convertPolicyAction(item.Action),
			Nexthops:    []string{item.NextHopIP},
			ExternalIDs: externalIDs,
		}
		if err = t.ovnNbClient.AddLogicalRouterPolicy(ctx, vpc.Name, policy); err != nil {
			logger.Error(err, "failed to add vpc logical route policy", "policy", item)
			return err
		}
	}

	return nil
}

func diffPolicyRouteWithExisted(exists, target []*ovnv1.PolicyRoute) ([]*ovnv1.PolicyRoute, []*ovnv1.PolicyRoute) {
	var (
		toDel, toAdd []*ovnv1.PolicyRoute
		existsMap    map[string]*ovnv1.PolicyRoute
		keyFunc      = func(route *ovnv1.PolicyRoute) string {
			return fmt.Sprintf("%d:%s:%s:%s", route.Priority, route.Match, route.Action, route.NextHopIP)
		}
	)

	existsMap = make(map[string]*ovnv1.PolicyRoute, len(exists))
	for _, item := range exists {
		existsMap[keyFunc(item)] = item
	}
	for _, item := range target {
		key := keyFunc(item)
		if _, ok := existsMap[key]; ok {
			delete(existsMap, key)
		} else {
			toAdd = append(toAdd, item)
		}
	}
	// load policies to delete
	for _, item := range existsMap {
		toDel = append(toDel, item)
	}
	return toDel, toAdd
}

func diffPolicyRouteWithExistedLogical(exists []*ovnnb.LogicalRouterPolicy, target []*ovnv1.PolicyRoute) ([]*ovnv1.PolicyRoute, []*ovnv1.PolicyRoute) {
	var (
		toDel, toAdd []*ovnv1.PolicyRoute
		existsMap    map[string]*ovnv1.PolicyRoute
		keyFunc      = func(route *ovnv1.PolicyRoute) string {
			return fmt.Sprintf("%d:%s:%s:%s", route.Priority, route.Match, route.Action, route.NextHopIP)
		}
	)
	existsMap = make(map[string]*ovnv1.PolicyRoute, len(exists))
	for _, item := range exists {
		policy := &ovnv1.PolicyRoute{
			Priority: item.Priority,
			Match:    item.Match,
			Action:   ovnv1.PolicyRouteAction(item.Action),
		}
		existsMap[keyFunc(policy)] = policy
	}

	for _, item := range target {
		key := keyFunc(item)

		if _, ok := existsMap[key]; ok {
			delete(existsMap, key)
		} else {
			toAdd = append(toAdd, item)
		}
	}

	for _, item := range existsMap {
		toDel = append(toDel, item)
	}
	return toDel, toAdd
}

func (t *vpcTranslator) CreateVpcLoadBalancers(ctx context.Context, vpc *ovnv1.Vpc) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	if err = t.createLoadBalancer(ctx, vpc.Status.TCPLoadBalancer, consts.ProtocolTcp, false); err != nil {
		logger.Error(err, "failed to create load balancer", "lb", vpc.Status.TCPLoadBalancer)
		return err
	}
	if err = t.createLoadBalancer(ctx, vpc.Status.UDPLoadBalancer, consts.ProtocolUdp, false); err != nil {
		logger.Error(err, "failed to create load balancer", "lb", vpc.Status.UDPLoadBalancer)
		return err
	}
	if err = t.createLoadBalancer(ctx, vpc.Status.SctpLoadBalancer, consts.ProtocolSctp, false); err != nil {
		logger.Error(err, "failed to create load balancer", "lb", vpc.Status.SctpLoadBalancer)
		return err
	}
	if err = t.createLoadBalancer(ctx, vpc.Status.TCPSessionLoadBalancer, consts.ProtocolTcp, true); err != nil {
		logger.Error(err, "failed to create load balancer", "lb", vpc.Status.TCPSessionLoadBalancer)
		return err
	}
	if err = t.createLoadBalancer(ctx, vpc.Status.UDPSessionLoadBalancer, consts.ProtocolUdp, true); err != nil {
		logger.Error(err, "failed to create load balancer", "lb", vpc.Status.UDPSessionLoadBalancer)
		return err
	}
	if err = t.createLoadBalancer(ctx, vpc.Status.SctpSessionLoadBalancer, consts.ProtocolSctp, true); err != nil {
		logger.Error(err, "failed to create load balancer", "lb", vpc.Status.SctpSessionLoadBalancer)
		return err
	}

	return nil
}

func (t *vpcTranslator) createLoadBalancer(ctx context.Context, lbName, protocol string, sessionAffinity bool) error {
	var (
		logger = log.FromContext(ctx).WithValues("lb", lbName)
		err    error
	)

	var lb *ovnnb.LoadBalancer
	if lb, err = t.ovnNbClient.GetLoadBalancer(ctx, lbName, true); err != nil {
		logger.Error(err, "failed to get load balancer")
		return err
	}

	if lb == nil {
		lb = &ovnnb.LoadBalancer{
			Name:     lbName,
			Protocol: &protocol,
		}
		if sessionAffinity {
			lb.SelectionFields = []string{ovnnb.LoadBalancerSelectionFieldsIPSrc}
			lb.Options = map[string]string{
				"affinity_timeout": strconv.Itoa(consts.DefaultServiceSessionAffinityTimeout),
			}
		}

		if err = t.ovnNbClient.CreateLoadBalancer(ctx, lb); err != nil {
			logger.Error(err, "failed to create load balancer")
			return err
		}
	}

	return nil
}

func (t *vpcTranslator) DeleteVpcLoadBalancers(ctx context.Context, vpc *ovnv1.Vpc) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	if err = t.ovnNbClient.DeleteLoadBalancer(ctx, vpc.Status.TCPLoadBalancer); err != nil {
		logger.Error(err, "failed to delete load balancer", "lb", vpc.Status.TCPLoadBalancer)
		return err
	}
	if err = t.ovnNbClient.DeleteLoadBalancer(ctx, vpc.Status.UDPLoadBalancer); err != nil {
		logger.Error(err, "failed to delete load balancer", "lb", vpc.Status.UDPLoadBalancer)
		return err
	}
	if err = t.ovnNbClient.DeleteLoadBalancer(ctx, vpc.Status.SctpLoadBalancer); err != nil {
		logger.Error(err, "failed to delete load balancer", "lb", vpc.Status.SctpLoadBalancer)
		return err
	}
	if err = t.ovnNbClient.DeleteLoadBalancer(ctx, vpc.Status.TCPSessionLoadBalancer); err != nil {
		logger.Error(err, "failed to delete load balancer", "lb", vpc.Status.TCPSessionLoadBalancer)
		return err
	}
	if err = t.ovnNbClient.DeleteLoadBalancer(ctx, vpc.Status.UDPSessionLoadBalancer); err != nil {
		logger.Error(err, "failed to delete load balancer", "lb", vpc.Status.UDPSessionLoadBalancer)
		return err
	}
	if err = t.ovnNbClient.DeleteLoadBalancer(ctx, vpc.Status.SctpSessionLoadBalancer); err != nil {
		logger.Error(err, "failed to delete load balancer", "lb", vpc.Status.SctpSessionLoadBalancer)
		return err
	}

	return nil
}

func (t *vpcTranslator) DeleteVpcRouter(ctx context.Context, vpc *ovnv1.Vpc) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)
	if err = t.ovnNbClient.DeleteLogicalRouter(ctx, vpc.Status.Router); err != nil {
		logger.Error(err, "failed to delete vpc router")
		return err
	}

	return nil
}
