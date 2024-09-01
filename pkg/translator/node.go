package translator

import (
	"context"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"

	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type nodeTranslator struct {
	translator
}

func (t *nodeTranslator) ReconcileNodeJoinNetwork(ctx context.Context, nodeName, v4NodeIP, v6NodeIP, defaultRouter, JoinSubnetName, ips, mac string) error {
	var (
		logger   = log.FromContext(ctx)
		err      error
		portName = fmt.Sprintf("node-%s", nodeName)
	)

	// 1. create logical switch port for node, connect to join switch
	if err = t.ovnNbClient.CreateBareLogicalSwitchPort(ctx, JoinSubnetName, portName, ips, mac); err != nil {
		logger.Error(err, "failed to create logical switch port", "ls", JoinSubnetName, "lsp", portName)
		return err
	}

	// 2. create policy route for node, route dst traffic to node
	for _, ip := range strings.Split(ips, ",") {
		switch utils.CheckProtocol(ip) {
		case utils.ProtocolIPv4:
			if err = t.MigrateNodeRoute(ctx, nodeName, 4, defaultRouter, v4NodeIP, ip); err != nil {
				logger.Error(err, "failed to migrate v4 policy route for node", "node", nodeName)
				return err
			}
		case utils.ProtocolIPv6:
			if err = t.MigrateNodeRoute(ctx, nodeName, 6, defaultRouter, v6NodeIP, ip); err != nil {
				logger.Error(err, "failed to migrate v6 policy route for node", "node", nodeName)
				return err
			}

		}
	}

	// 3. create port group for node, store ports of pods on this node
	pgName := formatModelName(portName)
	externalIDs := map[string]string{
		consts.ExternalIDsKeyNetworkPolicy: "node/" + nodeName,
	}
	if err = t.ovnNbClient.CreatePortGroup(ctx, pgName, externalIDs); err != nil {
		logger.Error(err, "failed to create port group for node", "node", nodeName, "portGroup", pgName)
		return err
	}

	return nil
}

// MigrateNodeRoute help pods to call on nodes
func (t *nodeTranslator) MigrateNodeRoute(ctx context.Context, nodeName string, af int, router, nodeIP, nextHop string) error {
	var (
		logger = log.FromContext(ctx)

		policy = ovnnb.LogicalRouterPolicy{
			Priority: consts.PolicyRoutePriorityNode,
			Match:    fmt.Sprintf("ip%d.dst == %s", af, nodeIP),
			Action:   ovnnb.LogicalRouterPolicyActionReroute,
			Nexthops: []string{nextHop},
			ExternalIDs: map[string]string{
				"vendor": consts.CniVendorName,
				"node":   nodeName,
			},
		}
	)
	logger.V(3).Info("add policy route to router", "router", router, "policy", policy)
	if err := t.ovnNbClient.AddLogicalRouterPolicy(ctx, router, &policy); err != nil {
		logger.Error(err, "failed to add logical router policy for node", "node", nodeName)
		return err
	}

	return nil
}

func (t *nodeTranslator) CleanupNodeJoinNetwork(ctx context.Context, nodeName, defaultVpc string, nextHops []string) error {
	var (
		logger = log.FromContext(ctx)

		err error
	)

	portName := fmt.Sprintf("node-%s", nodeName)
	// del lsp for node
	if err = t.ovnNbClient.DeleteLogicalSwitchPort(ctx, portName); err != nil {
		logger.Error(err, "failed to delete logical switch port for node", "lsp", portName)
		return err
	}

	// del port group for node
	pgName := formatModelName(portName)
	if err = t.ovnNbClient.DeletePortGroup(ctx, pgName); err != nil {
		logger.Error(err, "delete port group for node", "portGroup")
		return err
	}

	for _, nextHop := range nextHops {
		filter := func(policy *ovnnb.LogicalRouterPolicy) bool {
			return policy.Priority == consts.PolicyRoutePriorityNode &&
				((policy.Nexthop != nil && *policy.Nexthop == nextHop) || utils.ContainsString(policy.Nexthops, nextHop))
		}
		if err = t.ovnNbClient.DeleteLogicalRouterPolicyByFilter(ctx, defaultVpc, filter); err != nil {
			logger.Error(err, "failed to delete logical router policy for node", "node", nodeName, "nextHop", nextHop)
			return err
		}
	}

	return nil
}
