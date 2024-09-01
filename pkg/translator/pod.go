package translator

import (
	"context"
	"fmt"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"

	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	v1 "k8s.io/api/core/v1"
)

type podTranslator struct {
	translator
}

func (t *podTranslator) ReconcilePodPorts(ctx context.Context, pod *v1.Pod, targetPortNames map[string]int) (map[string]string, []string, error) {
	var (
		logger = log.FromContext(ctx)

		key = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	)

	filter := func(lsp *ovnnb.LogicalSwitchPort) bool {
		return lsp.ExternalIDs != nil &&
			lsp.ExternalIDs[consts.ExternalIDsKeyVendor] == consts.CniVendorName &&
			lsp.ExternalIDs[consts.ExternalIDsKeyPod] == key
	}

	existingPorts, err := t.ovnNbClient.ListLogicalSwitchPorts(ctx, filter)
	if err != nil {
		logger.Error(err, "failed to list logical switch ports")
		return nil, nil, err
	}

	portsToDel := make(map[string]string) // portName:subnetName
	var providerToDel []string
	for _, port := range existingPorts {
		if _, ok := targetPortNames[port.Name]; !ok {
			portsToDel[port.Name] = port.ExternalIDs[consts.ExternalIDsKeyLogicalSwitch]

			providerName := strings.Join(strings.Split(port.Name, ",")[2:], ",")
			if providerName == consts.OvnProviderName {
				continue
			} else {
				providerToDel = append(providerToDel, providerName)
			}
		}
	}

	if len(portsToDel) == 0 {
		return nil, nil, nil
	}

	for portName, _ := range portsToDel {
		if err = t.ovnNbClient.DeleteLogicalSwitchPort(ctx, portName); err != nil {
			logger.Error(err, "failed to delete logical switch port", "lsp", portName)
			return nil, nil, err
		}
	}

	return portsToDel, providerToDel, nil
}

func (t *podTranslator) UninstallPodNets(ctx context.Context, key string) error {
	var (
		logger = log.FromContext(ctx)
	)

	logger.Info("uninstall pod net", "key", key)

	filter := func(lsp *ovnnb.LogicalSwitchPort) bool {
		return lsp.ExternalIDs != nil &&
			lsp.ExternalIDs[consts.ExternalIDsKeyVendor] == consts.CniVendorName &&
			lsp.ExternalIDs[consts.ExternalIDsKeyPod] == key
	}

	existsPorts, err := t.ovnNbClient.ListLogicalSwitchPorts(ctx, filter)
	if err != nil {
		logger.Error(err, "failed to list logical switch ports")
		return err
	}

	var ops []ovsdb.Operation

	for _, port := range existsPorts {
		if op, err := t.ovnNbClient.DeleteLogicalSwitchPortOp(ctx, port.Name); err != nil {
			logger.Error(err, "failed to generate operations for deleting logical switch port", "lsp", port.Name)
			return err
		} else {
			ops = append(ops, op...)
		}
	}

	if err = t.ovnNbClient.Transact(ctx, "pod-lsp-del", ops); err != nil {
		return err
	}

	return nil
}

func (t *podTranslator) AddPodToSubnet(ctx context.Context, pod *v1.Pod, subnetName, netProvider string) error {
	var (
		logger   = log.FromContext(ctx)
		nodeName = pod.Spec.NodeName
	)

	if nodeName == "" {
		return fmt.Errorf("pod %s/%s has no node name", pod.Namespace, pod.Name)
	}
	pgName := OverlaySubnetsPortGroupName(subnetName, nodeName)
	portName := PodNameToPortName(pod.Name, pod.Namespace, netProvider)

	logger.Info("add port to port group", "port", portName, "pg", pgName)

	if err := t.ovnNbClient.AddPortGroupPorts(ctx, pgName, portName); err != nil {
		logger.Error(err, "failed to add port to port group", "port", portName, "portGroup", pgName)
		return err
	}

	return nil
}

type PodNetInfo struct {
	PodName      string
	PodNamespace string
	Subnet       string
	IPs          string
	Mac          string
	Vpc          string
	Provider     string
}

func (t *podTranslator) InstallPodNet(ctx context.Context, info PodNetInfo) error {
	var (
		logger = log.FromContext(ctx)

		lsName  = info.Subnet
		lspName = PodNameToPortName(info.PodName, info.PodNamespace, info.Provider)
	)

	existingLsp, err := t.ovnNbClient.GetLogicalSwitchPort(ctx, lspName, true)
	if err != nil {
		logger.Error(err, "failed to get logical switch port", "lsp", lspName)
		return err
	}

	var ops []ovsdb.Operation

	if existingLsp != nil {
		logger.Info("existing lsp", "UUID", existingLsp.UUID)
		lsUpdateMutation := func(ls *ovnnb.LogicalSwitch) *model.Mutation {
			return &model.Mutation{
				Field:   &ls.Ports,
				Mutator: ovsdb.MutateOperationDelete,
				Value:   []string{existingLsp.UUID},
			}
		}
		if ops, err = t.ovnNbClient.MutateLogicalSwitchOp(ctx, lsName, lsUpdateMutation); err != nil {
			logger.Error(err, "failed to generate operations for deleting logical switch port", "ls", lspName, "lsp", lspName)
			return err
		}
	}

	ips := strings.Split(info.IPs, ",")
	addresses := make([]string, 0, len(ips)+1)
	addresses = append(addresses, info.Mac)
	addresses = append(addresses, ips...)

	lsp := &ovnnb.LogicalSwitchPort{
		UUID:         t.ovnNbClient.NamedUUID(),
		Name:         lspName,
		Addresses:    []string{strings.TrimSpace(strings.Join(addresses, " "))},
		PortSecurity: []string{strings.TrimSpace(strings.Join(addresses, " "))},
		ExternalIDs: map[string]string{
			consts.ExternalIDsKeyVendor:        consts.CniVendorName,
			consts.ExternalIDsKeyLogicalSwitch: lsName,
		},
	}
	if len(info.PodName) != 0 && len(info.PodNamespace) != 0 {
		lsp.ExternalIDs[consts.ExternalIDsKeyPod] = info.PodNamespace + "/" + info.PodName
	}
	lspCreateOps, err := t.ovnNbClient.CreateLogicalSwitchPortOp(ctx, lsName, lsp)
	if err != nil {
		logger.Error(err, "failed to generate operations for creating logical switch port", "ls", lsName, "lsp", lspName)
		return err
	}

	ops = append(ops, lspCreateOps...)
	if err = t.ovnNbClient.Transact(ctx, "lsp-add", ops); err != nil {
		logger.Error(err, "failed to add logical switch port", "ls", lsName, "lsp", lspName)
		return err
	}

	return nil
}
