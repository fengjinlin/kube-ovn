package ovs

import (
	"context"
	"errors"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"

	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
)

func (c *OvnNbClient) GetLogicalRouterPort(ctx context.Context, lrpName string, ignoreNotFound bool) (*ovnnb.LogicalRouterPort, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	lrp := &ovnnb.LogicalRouterPort{Name: lrpName}
	if err := c.Get(ctx, lrp); err != nil {
		if errors.Is(err, client.ErrNotFound) && ignoreNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get logical router port %s: %v", lrpName, err)
	}
	return lrp, nil
}

func (c *OvnNbClient) CreateLogicalRouterPortOp(ctx context.Context, lrName string, lrp *ovnnb.LogicalRouterPort) ([]ovsdb.Operation, error) {
	if lrp == nil {
		return nil, fmt.Errorf("logical router port is nil")
	}

	if lrp.ExternalIDs == nil {
		lrp.ExternalIDs = make(map[string]string)
	}
	lrp.ExternalIDs[consts.ExternalIDsKeyLogicalRouter] = lrName
	lrp.ExternalIDs[consts.ExternalIDsKeyVendor] = consts.CniVendorName

	lrpCreateOp, err := c.Create(lrp)
	if err != nil {
		return nil, fmt.Errorf("generate operations for creating logical router port %s: %v", lrp.Name, err)
	}

	lrUpdateMutation := func(lr *ovnnb.LogicalRouter) *model.Mutation {
		return &model.Mutation{
			Field:   &lr.Ports,
			Mutator: ovsdb.MutateOperationInsert,
			Value:   []string{lrp.UUID},
		}
	}
	lrpAddOp, err := c.MutateLogicalRouterOps(ctx, lrName, lrUpdateMutation)
	if err != nil {
		return nil, fmt.Errorf("generate operations for adding logical router port %s: %v", lrp.Name, err)
	}

	ops := make([]ovsdb.Operation, 0, len(lrpCreateOp)+len(lrpAddOp))
	ops = append(ops, lrpCreateOp...)
	ops = append(ops, lrpAddOp...)

	return ops, nil
}

func (c *OvnNbClient) UpdateLogicalRouterPortOp(ctx context.Context, lrp *ovnnb.LogicalRouterPort, fields ...interface{}) ([]ovsdb.Operation, error) {
	if lrp == nil {
		return nil, fmt.Errorf("logical router port is nil")
	}
	op, err := c.Where(lrp).Update(lrp, fields...)
	if err != nil {
		return nil, fmt.Errorf("generate operations for updating logical router port %s: %v", lrp.Name, err)
	}
	return op, nil
}

func (c *OvnNbClient) UpdateLogicalRouterPort(ctx context.Context, lrp *ovnnb.LogicalRouterPort, fields ...interface{}) error {
	op, err := c.UpdateLogicalRouterPortOp(ctx, lrp, fields...)
	if err != nil {
		return err
	}
	if err = c.Transact(ctx, "lrp-update", op); err != nil {
		return fmt.Errorf("update logical router port %s: %v", lrp.Name, err)
	}

	return nil
}

func (c *OvnNbClient) DeleteLogicalRouterPortOp(ctx context.Context, lrpName string) ([]ovsdb.Operation, error) {
	lrp, err := c.GetLogicalRouterPort(ctx, lrpName, true)
	if err != nil {
		return nil, err
	}
	if lrp == nil {
		return nil, nil
	}

	lrName := lrp.ExternalIDs[consts.ExternalIDsKeyLogicalRouter]
	if len(lrName) == 0 {
		return nil, fmt.Errorf("external id %s is nil, lrp %s", consts.ExternalIDsKeyLogicalRouter, lrp.Name)
	}

	lrUpdateMutation := func(lr *ovnnb.LogicalRouter) *model.Mutation {
		return &model.Mutation{
			Field:   &lr.Ports,
			Mutator: ovsdb.MutateOperationDelete,
			Value:   []string{lrp.UUID},
		}
	}

	ops, err := c.MutateLogicalRouterOps(ctx, lrName, lrUpdateMutation)
	if err != nil {
		return nil, fmt.Errorf("generate operations for removeing logical router port %s: %v", lrName, err)
	}

	return ops, nil
}
