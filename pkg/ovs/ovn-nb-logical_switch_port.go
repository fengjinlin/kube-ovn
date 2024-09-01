package ovs

import (
	"context"
	"errors"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	"strings"
)

func (c *OvnNbClient) CreateLogicalSwitchPortOp(ctx context.Context, lsName string, lsp *ovnnb.LogicalSwitchPort) ([]ovsdb.Operation, error) {
	if lsp == nil {
		return nil, fmt.Errorf("logical switch port is nil")
	}

	if lsp.ExternalIDs == nil {
		lsp.ExternalIDs = make(map[string]string)
	}
	lsp.ExternalIDs[consts.ExternalIDsKeyLogicalSwitch] = lsName
	lsp.ExternalIDs[consts.ExternalIDsKeyVendor] = consts.CniVendorName

	lspCreateOp, err := c.Create(lsp)
	if err != nil {
		return nil, fmt.Errorf("generate operations for creating logical switch port %s: %v", lsp.Name, err)
	}

	lsUpdateMutation := func(ls *ovnnb.LogicalSwitch) *model.Mutation {
		return &model.Mutation{
			Field:   &ls.Ports,
			Mutator: ovsdb.MutateOperationInsert,
			Value:   []string{lsp.UUID},
		}
	}
	lspAddOp, err := c.MutateLogicalSwitchOp(ctx, lsName, lsUpdateMutation)
	if err != nil {
		return nil, fmt.Errorf("generate operations for adding logical switch port %s: %v", lsp.Name, err)
	}

	ops := make([]ovsdb.Operation, 0, len(lspCreateOp)+len(lspAddOp))
	ops = append(ops, lspCreateOp...)
	ops = append(ops, lspAddOp...)

	return ops, nil
}

func (c *OvnNbClient) DeleteLogicalSwitchPort(ctx context.Context, lspName string) error {
	ops, err := c.DeleteLogicalSwitchPortOp(ctx, lspName)
	if err != nil {
		return err
	}

	if err = c.Transact(ctx, "lsp-del", ops); err != nil {
		return err
	}

	return nil
}

func (c *OvnNbClient) DeleteLogicalSwitchPortOp(ctx context.Context, lspName string) ([]ovsdb.Operation, error) {
	lsp, err := c.GetLogicalSwitchPort(ctx, lspName, true)
	if err != nil {
		return nil, err
	}

	if lsp == nil {
		return nil, nil
	}

	lsName := lsp.ExternalIDs[consts.ExternalIDsKeyLogicalSwitch]
	if len(lsName) == 0 {
		return nil, fmt.Errorf("external id %s is nil, lsp %s", consts.ExternalIDsKeyLogicalSwitch, lsp.Name)
	}

	lsUpdateMutation := func(ls *ovnnb.LogicalSwitch) *model.Mutation {
		return &model.Mutation{
			Field:   &ls.Ports,
			Mutator: ovsdb.MutateOperationDelete,
			Value:   []string{lsp.UUID},
		}
	}

	ops, err := c.MutateLogicalSwitchOp(ctx, lsName, lsUpdateMutation)
	if err != nil {
		return nil, fmt.Errorf("generate operations for removing logical switch port %s: %v", lspName, err)
	}

	return ops, nil
}

func (c *OvnNbClient) CreateBareLogicalSwitchPort(ctx context.Context, lsName, lspName, ip, mac string) error {
	lsp, err := c.GetLogicalSwitchPort(ctx, lspName, true)
	if err != nil {
		return err
	}

	if lsp != nil {
		return nil
	}

	lsp = &ovnnb.LogicalSwitchPort{
		UUID:      c.NamedUUID(),
		Name:      lspName,
		Addresses: []string{fmt.Sprintf("%s %s", mac, strings.ReplaceAll(ip, ",", " "))},
	}

	ops, err := c.CreateLogicalSwitchPortOp(ctx, lsName, lsp)
	if err != nil {
		return err
	}

	if err = c.Transact(ctx, "lsp-add", ops); err != nil {
		return fmt.Errorf("create logical switch port %s: %v", lspName, err)
	}

	return nil
}

func (c *OvnNbClient) GetLogicalSwitchPort(ctx context.Context, lspName string, ignoreNotFound bool) (*ovnnb.LogicalSwitchPort, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	lsp := &ovnnb.LogicalSwitchPort{Name: lspName}
	if err := c.Get(ctx, lsp); err != nil {
		if errors.Is(err, client.ErrNotFound) && ignoreNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get logical switch port %s: %v", lspName, err)
	}
	return lsp, nil
}

func (c *OvnNbClient) ListLogicalSwitchPorts(ctx context.Context, filter func(lsp *ovnnb.LogicalSwitchPort) bool) ([]ovnnb.LogicalSwitchPort, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	results := make([]ovnnb.LogicalSwitchPort, 0)

	if err := c.WhereCache(filter).List(ctx, &results); err != nil {
		return nil, fmt.Errorf("list logical switch ports: %v", err)
	}

	return results, nil
}
