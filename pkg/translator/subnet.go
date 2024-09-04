package translator

import (
	"context"
	"errors"
	"fmt"

	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type subnetTranslator struct {
	translator
}

func (t *subnetTranslator) CreateSubnetSwitch(ctx context.Context, subnetName, vpcName, cidrBlock, gateway string, needRouter bool) error {
	lsName := subnetName
	lrName := vpcName
	lspName := fmt.Sprintf("%s-%s", lsName, lrName)
	lrpName := fmt.Sprintf("%s-%s", lrName, lsName)

	networks := utils.GetIPAddrWithMask(gateway, cidrBlock)

	logger := log.FromContext(ctx).WithValues("lsName", lsName, "lspName", lspName,
		"lrName", vpcName, "lrpName", lrpName, "networks", networks)

	logger.Info("creating logical switch to subnet")

	ls, err := t.ovnNbClient.GetLogicalSwitch(ctx, lsName, true)
	if err != nil {
		logger.Error(err, "failed to get logical switch")
		return err
	}

	if ls == nil {
		err = t.ovnNbClient.CreateLogicalSwitch(ctx, lsName)
		if err != nil {
			logger.Error(err, "failed to create logical switch")
			return err
		}
	}

	if needRouter {
		err = t.CreateLogicalPathPort(ctx, lsName, lspName, lrName, lrpName, networks, utils.GenerateMac())
		if err != nil {
			logger.Error(err, "failed to create logical path port")
			return err
		}
	} else {
		err = t.deleteLogicalPathPort(ctx, lspName, lrpName)
		if err != nil {
			logger.Error(err, "failed to delete logical path port")
			return err
		}
	}

	return nil
}

func (t *subnetTranslator) deleteLogicalPathPort(ctx context.Context, lspName, lrpName string) error {
	logger := log.FromContext(ctx)
	lspDelOps, err := t.ovnNbClient.DeleteLogicalSwitchPortOp(ctx, lspName)
	if err != nil {
		logger.Error(err, "failed to generate operations for deleting logical switch port", "lsp", lspName)
		return err
	}

	lrpDelOps, err := t.ovnNbClient.DeleteLogicalRouterPortOp(ctx, lrpName)
	if err != nil {
		logger.Error(err, "failed to generate operations for deleting logical router port", "lrp", lrpName)
		return err
	}

	ops := make([]ovsdb.Operation, 0, len(lspDelOps)+len(lrpDelOps))
	ops = append(ops, lspDelOps...)
	ops = append(ops, lrpDelOps...)

	if err = t.ovnNbClient.Transact(ctx, "lrp-lsp-del", ops); err != nil {
		logger.Error(err, "failed to delete logical patch port", "lsp", lspName, "lrp", lrpName)
		return err
	}
	return nil
}

func (t *subnetTranslator) CreateLogicalPathPort(ctx context.Context, lsName, lspName, lrName, lrpName, ip, mac string, chassis ...string) error {
	logger := log.FromContext(ctx)
	logger.Info("creating logical path port", "ls", lsName, "lspName", lspName, "lr", lrName, "lrp", lrpName, "ip", ip, "mac", mac, "chassis", chassis)

	if len(ip) != 0 {
		// check ip format: 192.168.231.1/24,fc00::0af4:01/112
		if err := utils.CheckCidrs(ip); err != nil {
			logger.Error(err, "failed to check cidr", "cidr", ip)
			return fmt.Errorf("invalid ip %s: %v", ip, err)
		}
	}

	lsp, err := t.ovnNbClient.GetLogicalSwitchPort(ctx, lspName, true)
	if err != nil {
		logger.Error(err, "failed to get logical switch", "lsp", lspName)
		return err
	}

	ops := make([]ovsdb.Operation, 0)

	if lsp == nil {
		lsp = &ovnnb.LogicalSwitchPort{
			UUID:      t.ovnNbClient.NamedUUID(),
			Name:      lspName,
			Addresses: []string{"router"},
			Type:      "router",
			Options: map[string]string{
				"router-port": lrpName,
			},
		}
		lspCreateOps, err := t.ovnNbClient.CreateLogicalSwitchPortOp(ctx, lsName, lsp)
		if err != nil {
			logger.Error(err, "failed to generate operations for creating logical switch port", "ls", lsName, "lsp", lspName)
			return err
		}
		ops = append(ops, lspCreateOps...)
	}

	lrp, err := t.ovnNbClient.GetLogicalRouterPort(ctx, lrpName, true)
	if err != nil {
		logger.Error(err, "failed to get logical router port", "lrp", lrpName)
		return err
	}

	if lrp == nil {
		lrp = &ovnnb.LogicalRouterPort{
			UUID:     t.ovnNbClient.NamedUUID(),
			Name:     lrpName,
			Networks: strings.Split(ip, ","),
			MAC:      mac,
		}
		lrpCreateOps, err := t.ovnNbClient.CreateLogicalRouterPortOp(ctx, lrName, lrp)
		if err != nil {
			logger.Error(err, "failed to generate operations for creating logical router port", "lr", lrName, "lrp", lrpName)
			return err
		}
		ops = append(ops, lrpCreateOps...)
	} else {
		lrp = &ovnnb.LogicalRouterPort{
			Name:     lrpName,
			Networks: strings.Split(ip, ","),
		}
		lrpUpdateOps, err := t.ovnNbClient.UpdateLogicalRouterPortOp(ctx, lrp, &lrp.Networks)
		if err != nil {
			logger.Error(err, "failed to generate operations for update logical router port", "lr", lrName, "lrp", lrpName, "networks", ip)
			return err
		}
		ops = append(ops, lrpUpdateOps...)
	}

	if len(ops) > 0 {
		if err = t.ovnNbClient.Transact(ctx, "lrp-lsp-add", ops); err != nil {
			logger.Error(err, "failed to create logical patch port", "lsp", lspName, "lrp", lrpName)
			return err
		}
	}

	// TODO: create gateway chassis

	return nil
}

func (t *subnetTranslator) ReconcileCommonPolicyRouteForSubnetInDefaultVpc(ctx context.Context, subnet *ovnv1.Subnet) error {
	var (
		logger = log.FromContext(ctx)
		err    error

		lrName      = subnet.Spec.Vpc
		priority    = consts.PolicyRoutePrioritySubnet
		externalIds = map[string]string{
			consts.ExternalIDsKeyVendor: consts.CniVendorName,
			consts.ExternalIDsKeySubnet: subnet.Name,
		}
	)

	// list all common policy route
	existingPolicies, err := t.ovnNbClient.ListLogicalRouterPolicy(ctx, lrName, func(policy *ovnnb.LogicalRouterPolicy) bool {
		if policy.Priority != priority {
			return false
		}
		for k, v := range externalIds {
			if policy.ExternalIDs == nil || policy.ExternalIDs[k] != v {
				return false
			}
		}
		return true
	})
	if err != nil {
		logger.Error(err, "failed to list existing logical router policy")
		return err
	}

	existingPolicyUUIDs := make(map[string]int, len(existingPolicies))
	for _, cidr := range strings.Split(subnet.Spec.CIDRBlock, ",") {
		if cidr == "" {
			continue
		}

		af := 4
		if utils.CheckProtocol(cidr) == utils.ProtocolIPv6 {
			af = 6
		}

		var (
			match    = fmt.Sprintf("ip%d.dst == %s", af, cidr)
			existing bool
		)

		for _, p := range existingPolicies {
			if p.Match == match {
				existing = true
				existingPolicyUUIDs[p.UUID] = 0
				break
			}
		}

		if !existing {
			policy := &ovnnb.LogicalRouterPolicy{
				Priority:    priority,
				Match:       match,
				Action:      ovnnb.LogicalRouterPolicyActionAllow,
				ExternalIDs: externalIds,
			}
			if err = t.ovnNbClient.AddLogicalRouterPolicy(ctx, lrName, policy); err != nil {
				logger.Error(err, "failed to add logical router policy", "policy", policy)
				return err
			}
		}
	}

	// remove invalid policies, cause by cidr changed
	for _, policy := range existingPolicies {
		if _, ok := existingPolicyUUIDs[policy.UUID]; !ok {
			if err = t.ovnNbClient.DeleteLogicalRouterPolicyByUUID(ctx, lrName, policy.UUID); err != nil {
				logger.Error(err, "failed to delete logical router policy", "policy", policy)
				return err
			}
		}
	}
	return nil
}

func (t *subnetTranslator) DeleteOutboundRouteForDistributedSubnetInDefaultVpc(ctx context.Context, vpcName, subnetName, subnetCIDR, nodeName string) error {
	var (
		logger = log.FromContext(ctx)
		err    error
		pgName string
	)

	logger.V(4).Info("delete outbound route for distributed subnet in default vpc", "subnet", subnetName, "node", nodeName)

	pgName = subnetName + "-" + nodeName
	pgName = formatModelName(pgName)
	if err = t.ovnNbClient.DeletePortGroup(ctx, pgName); err != nil {
		logger.Error(err, "failed to delete port group", "portGroup", pgName)
		return err
	}

	// del gateway policy route for node
	for _, cidr := range strings.Split(subnetCIDR, ",") {
		af := "ip4"
		if utils.CheckProtocol(cidr) == utils.ProtocolIPv6 {
			af = "ip6"
		}
		match := fmt.Sprintf("%s.src == %s_%s", af, pgName, af)
		filter := func(policy *ovnnb.LogicalRouterPolicy) bool {
			return policy.Priority == consts.PolicyRoutePriorityGateway && policy.Match == match
		}
		if err = t.ovnNbClient.DeleteLogicalRouterPolicyByFilter(ctx, vpcName, filter); err != nil {
			logger.Error(err, "failed to delete gateway policy route for subnet on node", "subnet", subnetName, "node", nodeName, "match", match)
			return err
		}
	}

	return nil
}

func (t *subnetTranslator) ReconcileOutboundRouteForDistributedSubnetInDefaultVpc(ctx context.Context, subnet *ovnv1.Subnet, node *corev1.Node) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	if subnet.Spec.GatewayType != consts.GatewayTypeDistributed {
		return errors.New("not a distributed subnet")
	}
	if node.Annotations == nil || node.Annotations[consts.AnnotationAllocated] != "true" {
		return errors.New("node has not been allocated")
	}

	var (
		defaultVpc  = subnet.Spec.Vpc
		subnetName  = subnet.Name
		nodeJoinIPs = node.Annotations[consts.AnnotationJoinIP]
		nodeName    = node.Name
	)

	// 1. delete old outbound route, which belong to this subnet, but not create for distributed gateway type
	{
		if err = t.ovnNbClient.DeleteLogicalRouterPolicyByFilter(ctx, defaultVpc, func(policy *ovnnb.LogicalRouterPolicy) bool {
			return policy.Priority == consts.PolicyRoutePriorityGateway &&
				policy.ExternalIDs != nil &&
				policy.ExternalIDs[consts.ExternalIDsKeySubnet] == subnetName &&
				policy.ExternalIDs[consts.ExternalIDsKeyNode] == ""
		}); err != nil {
			logger.Error(err, "failed to list logical router policy")
			return err
		}
	}

	// 2. create port group for subnet and node
	pgName := subnet.Name + "-" + nodeName
	pgName = formatModelName(pgName)
	externalIDs := map[string]string{
		consts.ExternalIDsKeyNetworkPolicy: subnet.Name + "/" + nodeName,
	}
	if err = t.ovnNbClient.CreatePortGroup(ctx, pgName, externalIDs); err != nil {
		logger.Error(err, "failed to create port group for subnet on node", "subnet", subnet.Name, "node", nodeName, err)
		return err
	}

	// 3. create policy route for port group
	for _, nodeJoinIP := range strings.Split(nodeJoinIPs, ",") {
		af := "ip4"
		if utils.CheckProtocol(nodeJoinIP) == utils.ProtocolIPv6 {
			af = "ip6"
		}
		var (
			pgAddressSet = fmt.Sprintf("%s_%s", pgName, af)

			policy = ovnnb.LogicalRouterPolicy{
				Priority: consts.PolicyRoutePriorityGateway,
				Match:    fmt.Sprintf("%s.src == $%s", af, pgAddressSet),
				Action:   ovnnb.LogicalRouterPolicyActionReroute,
				Nexthops: []string{nodeJoinIP},
				ExternalIDs: map[string]string{
					consts.ExternalIDsKeyVendor: consts.CniVendorName,
					consts.ExternalIDsKeySubnet: subnet.Name,
					consts.ExternalIDsKeyNode:   nodeName,
				},
			}
		)
		logger.Info("add logical router policy", "router", defaultVpc, "policy", policy)
		if err = t.ovnNbClient.AddLogicalRouterPolicy(ctx, defaultVpc, &policy); err != nil {
			logger.Error(err, "failed to add logical router policy")
			return err
		}

	}

	return nil
}

func (t *subnetTranslator) ReconcileOutboundRouteForCentralizedSubnetInDefaultVpc(ctx context.Context, subnet *ovnv1.Subnet, v4Nexthops, v6Nexthops []string, v4NodeNameIPMap, v6NodeNameIPMap map[string]string) error {
	var (
		logger     = log.FromContext(ctx)
		err        error
		subnetName = subnet.Name
		defaultVpc = subnet.Spec.Vpc
	)

	// 1. delete old outbound route, which belong to this subnet, but not create for centralized gateway type
	{
		var existingRoutes []*ovnnb.LogicalRouterPolicy
		existingRoutes, err = t.ovnNbClient.ListLogicalRouterPolicy(ctx, defaultVpc, func(policy *ovnnb.LogicalRouterPolicy) bool {
			return policy.Priority == consts.PolicyRoutePriorityGateway &&
				policy.ExternalIDs != nil &&
				policy.ExternalIDs[consts.ExternalIDsKeySubnet] == subnetName &&
				policy.ExternalIDs[consts.ExternalIDsKeyNode] != ""
		})
		if err != nil {
			logger.Error(err, "failed to list logical router policy")
			return err
		}
		for _, route := range existingRoutes {
			// delete port group
			nodeName := route.ExternalIDs[consts.ExternalIDsKeyNode]
			pgName := formatModelName(fmt.Sprintf("%s-%s", subnetName, nodeName))
			if err = t.ovnNbClient.DeletePortGroup(ctx, pgName); err != nil {
				logger.Error(err, "failed to delete port group for subnet on node", "pgName", pgName)
				return err
			}
			// delete policy route
			if err = t.ovnNbClient.DeleteLogicalRouterPolicyByUUID(ctx, defaultVpc, route.UUID); err != nil {
				logger.Error(err, "failed to delete logical router policy for cleaning old outbound route", "uuid", route.UUID)
				return err
			}
		}
	}

	// 2. reconcile outbound route for centralized subnet

	for _, cidr := range strings.Split(subnet.Spec.CIDRBlock, ",") {
		af := "ip4"
		nexthops := v4Nexthops
		nodeNameIPMap := v4NodeNameIPMap
		if utils.CheckProtocol(cidr) == utils.ProtocolIPv6 {
			af = "ip6"
			nexthops = v6Nexthops
			nodeNameIPMap = v6NodeNameIPMap
		}
		if len(nexthops) > 0 {
			externalIds := map[string]string{
				consts.ExternalIDsKeyVendor: consts.CniVendorName,
				consts.ExternalIDsKeySubnet: subnet.Name,
			}
			for nodeName, gwIP := range nodeNameIPMap {
				externalIds[nodeName] = gwIP
			}
			policy := ovnnb.LogicalRouterPolicy{
				Priority:    consts.PolicyRoutePriorityGateway,
				Match:       fmt.Sprintf("%s.src == %s", af, cidr),
				Action:      ovnnb.LogicalRouterPolicyActionReroute,
				Nexthops:    nexthops,
				ExternalIDs: externalIds,
			}
			if err = t.ovnNbClient.AddLogicalRouterPolicy(ctx, defaultVpc, &policy); err != nil {
				return err
			}
		}
	}

	return nil
}

func (t *subnetTranslator) CleanupSubnetCommonPolicyRoute(ctx context.Context, subnet *ovnv1.Subnet) error {
	return t.ovnNbClient.DeleteLogicalRouterPolicyByFilter(ctx, subnet.Spec.Vpc, func(policy *ovnnb.LogicalRouterPolicy) bool {
		return policy.Priority == consts.PolicyRoutePrioritySubnet &&
			policy.ExternalIDs != nil &&
			policy.ExternalIDs[consts.ExternalIDsKeySubnet] == subnet.Name
	})
}

func (t *subnetTranslator) CleanupSubnetOutboundPolicyRoute(ctx context.Context, subnet *ovnv1.Subnet, nodeList *corev1.NodeList) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	if err = t.ovnNbClient.DeleteLogicalRouterPolicyByFilter(ctx, subnet.Spec.Vpc, func(policy *ovnnb.LogicalRouterPolicy) bool {
		return policy.Priority == consts.PolicyRoutePriorityGateway &&
			policy.ExternalIDs != nil &&
			policy.ExternalIDs[consts.ExternalIDsKeySubnet] == subnet.Name
	}); err != nil {
		logger.Error(err, "failed to delete outbound policy route")
		return err
	}

	if subnet.Spec.GatewayType == consts.GatewayTypeDistributed {
		for _, node := range nodeList.Items {
			pgName := formatModelName(fmt.Sprintf("%s-%s", subnet.Name, node.Name))
			if err = t.ovnNbClient.DeletePortGroup(ctx, pgName); err != nil {
				logger.Error(err, "failed to delete port group", "pgName", pgName)
				return err
			}
		}
	}

	return nil
}

func (t *subnetTranslator) DeleteSubnetSwitch(ctx context.Context, subnet *ovnv1.Subnet, router string) error {
	var (
		logger = log.FromContext(ctx)
		err    error

		lsName  = subnet.Name
		lspName = fmt.Sprintf("%s-%s", lsName, router)
		lrpName = fmt.Sprintf("%s-%s", router, lsName)
	)

	if err = t.ovnNbClient.DeleteLogicalSwitch(ctx, lsName); err != nil {
		logger.Error(err, "failed to delete subnet logical switch")
	}

	if err = t.deleteLogicalPathPort(ctx, lspName, lrpName); err != nil {
		logger.Error(err, "failed to delete logical path port")
		return err
	}
	return nil
}

func (t *subnetTranslator) EnableSubnetLoadBalancer(ctx context.Context, subnetName string, enable bool, lbNames ...string) error {
	var (
		logger = log.FromContext(ctx).WithValues("subnet", subnetName, "enable", enable, "lbs", lbNames)
		err    error
	)

	var lbUUIDs []string
	var lb *ovnnb.LoadBalancer
	for _, lbName := range lbNames {
		if lb, err = t.ovnNbClient.GetLoadBalancer(ctx, lbName, true); err != nil {
			logger.Error(err, "failed to get load balancer", "lb", lbName)
			return err
		}
		if lb == nil {
			if enable {
				return fmt.Errorf("load balancer `%s` not found", lbName)
			}
			continue
		}
		lbUUIDs = append(lbUUIDs, lb.UUID)
	}

	mutator := ovsdb.MutateOperationInsert
	if !enable {
		mutator = ovsdb.MutateOperationDelete
	}

	mutationFunc := func(ls *ovnnb.LogicalSwitch) *model.Mutation {
		return &model.Mutation{
			Field:   &ls.LoadBalancer,
			Mutator: mutator,
			Value:   lbUUIDs,
		}
	}

	var ops []ovsdb.Operation
	if ops, err = t.ovnNbClient.MutateLogicalSwitchOp(ctx, subnetName, mutationFunc); err != nil {
		logger.Error(err, "failed to generate operations for mutate load balancer of logical switch")
		return err
	}

	if err = t.ovnNbClient.Transact(ctx, "ls-lb-mutate", ops); err != nil {
		logger.Error(err, "failed to mutate load balancer of logical switch")
		return err
	}

	return nil
}

func (t *subnetTranslator) ReconcileSubnetACL(ctx context.Context, subnet *ovnv1.Subnet, joinSubnetCIDRs string) error {
	var (
		logger = log.FromContext(ctx)
		err    error

		lsName = subnet.Name
	)

	logger.Info("reconcile subnet acl")

	var targetACLs, existingACLs []*ovnnb.ACL
	targetACLs, _ = t.subnetTargetACLs(ctx, subnet, joinSubnetCIDRs)
	if existingACLs, err = t.subnetExistingACLs(ctx, subnet); err != nil {
		logger.Error(err, "failed to get subnet existing ACLs")
		return err
	}

	var (
		aclKeyFunc = func(acl *ovnnb.ACL) string {
			return fmt.Sprintf("%d##%s##%s##%s", acl.Priority, acl.Direction, acl.Match, acl.Action)
		}
		aclToAdd, aclToDel []*ovnnb.ACL
		existingMap        = make(map[string]*ovnnb.ACL)
	)

	for _, acl := range existingACLs {
		existingMap[aclKeyFunc(acl)] = acl
	}
	for _, acl := range targetACLs {
		key := aclKeyFunc(acl)
		if _, ok := existingMap[key]; ok {
			delete(existingMap, key)
		} else {
			aclToAdd = append(aclToAdd, acl)
		}
	}
	for _, acl := range existingMap {
		aclToDel = append(aclToDel, acl)
	}

	for _, acl := range aclToDel {
		if err = t.ovnNbClient.DeleteSwitchACL(ctx, lsName, acl.UUID); err != nil {
			logger.Error(err, "failed to delete subnet switch ACL")
			return err
		}
	}

	for _, acl := range aclToAdd {
		if err = t.ovnNbClient.CreateSwitchACL(ctx, lsName, acl); err != nil {
			logger.Error(err, "failed to add subnet switch ACL")
			return err
		}
	}

	return nil
}

func (t *subnetTranslator) subnetTargetACLs(_ context.Context, subnet *ovnv1.Subnet, joinSubnetCIDRs string) ([]*ovnnb.ACL, error) {
	var (
		lsName      = subnet.Name
		externalIDs = map[string]string{
			consts.AclKeyParent: lsName,
		}
	)

	var targetACLs []*ovnnb.ACL

	if subnet.Spec.Private {
		// default drop
		targetACLs = append(targetACLs, &ovnnb.ACL{
			UUID:        t.ovnNbClient.NamedUUID(),
			Priority:    consts.AclPriorityDefaultDrop,
			Direction:   ovnnb.ACLDirectionToLport,
			Match:       "ip",
			Action:      ovnnb.ACLActionDrop,
			ExternalIDs: externalIDs,

			Name:     &lsName,
			Log:      true,
			Severity: &ovnnb.ACLSeverityWarning,
		})

		for _, cidr := range strings.Split(subnet.Spec.CIDRBlock, ",") {
			protocol := utils.CheckProtocol(cidr)
			ipSuffix := "ip4"
			if protocol == utils.ProtocolIPv6 {
				ipSuffix = "ip6"
			}

			// allow same subnet
			targetACLs = append(targetACLs, &ovnnb.ACL{
				UUID:        t.ovnNbClient.NamedUUID(),
				Priority:    consts.AclPriorityAllowSubnet,
				Direction:   ovnnb.ACLDirectionToLport,
				Match:       fmt.Sprintf("%s.src == %s && %s.dst == %s", ipSuffix, cidr, ipSuffix, cidr),
				Action:      ovnnb.ACLActionAllowRelated,
				ExternalIDs: externalIDs,
			})

			// allow join subnet
			for _, joinCIDR := range strings.Split(joinSubnetCIDRs, "") {
				if utils.CheckProtocol(joinCIDR) != protocol {
					continue
				}

				targetACLs = append(targetACLs, &ovnnb.ACL{
					UUID:        t.ovnNbClient.NamedUUID(),
					Priority:    consts.AclPriorityAllowJoinSubnet,
					Direction:   ovnnb.ACLDirectionToLport,
					Match:       fmt.Sprintf("%s.src == %s", ipSuffix, joinCIDR),
					Action:      ovnnb.ACLActionAllowRelated,
					ExternalIDs: externalIDs,
				})
			}

			// allow subnets
			for _, allowCIDR := range subnet.Spec.AllowSubnets {
				if utils.CheckProtocol(allowCIDR) != protocol {
					continue
				}

				targetACLs = append(targetACLs, &ovnnb.ACL{
					UUID:      t.ovnNbClient.NamedUUID(),
					Priority:  consts.AclPriorityAllowJoinSubnet,
					Direction: ovnnb.ACLDirectionToLport,
					Match: fmt.Sprintf("(%s.src == %s && %s.dst == %s) || (%s.src == %s && %s.dst == %s)",
						ipSuffix, cidr, ipSuffix, allowCIDR,
						ipSuffix, allowCIDR, ipSuffix, cidr,
					),
					Action:      ovnnb.ACLActionAllowRelated,
					ExternalIDs: externalIDs,
				})
			}
		}
	}

	subnetExternalIDs := externalIDs
	subnetExternalIDs[consts.ExternalIDsKeySubnet] = subnet.Name
	for _, acl := range subnet.Spec.Acls {
		targetACLs = append(targetACLs, &ovnnb.ACL{
			UUID:        t.ovnNbClient.NamedUUID(),
			Priority:    acl.Priority,
			Direction:   acl.Direction,
			Match:       acl.Match,
			Action:      acl.Action,
			ExternalIDs: subnetExternalIDs,
		})
	}

	return targetACLs, nil
}

func (t *subnetTranslator) subnetExistingACLs(ctx context.Context, subnet *ovnv1.Subnet) ([]*ovnnb.ACL, error) {
	var (
		logger = log.FromContext(ctx)
	)

	ls, err := t.ovnNbClient.GetLogicalSwitch(ctx, subnet.Name, false)
	if err != nil {
		logger.Error(err, "failed to get subnet logical switch")
		return nil, err
	}

	if len(ls.ACLs) > 0 {
		existingACLs := make([]*ovnnb.ACL, 0, len(ls.ACLs))
		for _, uuid := range ls.ACLs {
			if acl, err := t.ovnNbClient.GetACLByUUID(ctx, uuid); err != nil {
				logger.Error(err, "failed to get acl by uuid", "uuid", uuid)
				return nil, err
			} else {
				existingACLs = append(existingACLs, acl)
			}
		}

		return existingACLs, nil
	}

	return nil, nil
}
