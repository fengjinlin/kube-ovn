package ovs

import (
	"context"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func (c *OvnNbClient) CreateLogicalSwitch(ctx context.Context, lsName string) error {
	ls := &ovnnb.LogicalSwitch{
		Name:        lsName,
		ExternalIDs: map[string]string{"vendor": consts.CniVendorName},
	}

	op, err := c.ovsDbClient.Create(ls)
	if err != nil {
		return fmt.Errorf("generate operations for creating logical switch %s: %v", lsName, err)
	}

	if err := c.Transact(ctx, "ls-add", op); err != nil {
		return err
	}
	return nil
}

func (c *OvnNbClient) GetLogicalSwitch(ctx context.Context, lsName string, ignoreNotFound bool) (*ovnnb.LogicalSwitch, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	lsList := make([]ovnnb.LogicalSwitch, 0)
	if err := c.ovsDbClient.WhereCache(func(ls *ovnnb.LogicalSwitch) bool { return ls.Name == lsName }).List(ctx, &lsList); err != nil {
		return nil, fmt.Errorf("list switch switch %q: %v", lsName, err)
	}

	if len(lsList) == 0 {
		if ignoreNotFound {
			return nil, nil
		} else {
			return nil, fmt.Errorf("not found logical switch %q", lsName)
		}
	}

	return &lsList[0], nil
}

func (c *OvnNbClient) MutateLogicalSwitchOp(ctx context.Context, lsName string, mutationFunc ...func(ls *ovnnb.LogicalSwitch) *model.Mutation) ([]ovsdb.Operation, error) {
	if len(mutationFunc) == 0 {
		return nil, nil
	}

	ls, err := c.GetLogicalSwitch(ctx, lsName, false)
	if err != nil {
		return nil, fmt.Errorf("get logical switch %s: %v", lsName, err)
	}

	mutations := make([]model.Mutation, 0, len(mutationFunc))
	for _, fn := range mutationFunc {
		mutation := fn(ls)
		if mutation != nil {
			mutations = append(mutations, *mutation)
		}
	}

	ops, err := c.ovsDbClient.Where(ls).Mutate(ls, mutations...)
	if err != nil {
		return nil, fmt.Errorf("generate operations for mutating logical switch %s: %v", lsName, err)
	}

	return ops, nil
}

func (c *OvnNbClient) DeleteLogicalSwitch(ctx context.Context, lsName string) error {
	logger := log.FromContext(ctx).WithValues("ls", lsName)

	ls, err := c.GetLogicalSwitch(ctx, lsName, true)
	if err != nil {
		logger.Error(err, "failed to get logical switch when delete")
		return err
	}

	// not found, skip
	if ls == nil {
		return nil
	}

	op, err := c.Where(ls).Delete()
	if err != nil {
		logger.Error(err, "failed to generate operations for deleting logical switch")
		return err
	}

	if err = c.Transact(ctx, "ls-del", op); err != nil {
		return err
	}

	return nil
}
