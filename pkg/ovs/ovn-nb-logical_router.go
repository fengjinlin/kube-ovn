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

func (c *OvnNbClient) CreateLogicalRouter(ctx context.Context, lrName string) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	lr, err := c.GetLogicalRouter(ctx, lrName, true)
	if err != nil {
		return err
	}

	if lr != nil {
		return nil
	}

	lr = &ovnnb.LogicalRouter{
		Name:        lrName,
		ExternalIDs: map[string]string{"vendor": consts.CniVendorName},
	}
	ops, err := c.ovsDbClient.Create(lr)
	if err != nil {
		logger.Error(err, "failed to generate operations for creating logical router", "lr", lrName)
		return err
	}

	if err = c.Transact(ctx, "lr-add", ops); err != nil {
		return err
	}

	return nil
}

func (c *OvnNbClient) DeleteLogicalRouter(ctx context.Context, lrName string) error {
	logger := log.FromContext(ctx)

	lr, err := c.GetLogicalRouter(ctx, lrName, true)
	if err != nil {
		logger.Error(err, "failed to get logical router when delete", "lr", lrName)
		return err
	}

	// not found, skip
	if lr == nil {
		return nil
	}

	op, err := c.Where(lr).Delete()
	if err != nil {
		logger.Error(err, "failed to generate operations for deleting logical router", "lr", lrName)
		return err
	}

	if err = c.Transact(ctx, "lr-del", op); err != nil {
		return err
	}

	return nil
}

func (c *OvnNbClient) GetLogicalRouter(ctx context.Context, lrName string, ignoreNotFound bool) (*ovnnb.LogicalRouter, error) {
	logger := log.FromContext(ctx)

	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	lrs := make([]ovnnb.LogicalRouter, 0)
	if err := c.ovsDbClient.WhereCache(func(lr *ovnnb.LogicalRouter) bool { return lr.Name == lrName }).List(ctx, &lrs); err != nil {
		logger.Error(err, "failed to list logical router", "lr", lrName)
		return nil, err
	}
	if len(lrs) == 0 {
		if ignoreNotFound {
			return nil, nil
		} else {
			return nil, fmt.Errorf("no logical router found with name %q", lrName)
		}
	}

	if len(lrs) > 1 {
		return nil, fmt.Errorf("found more than one logical router with name %q", lrName)
	}

	return &lrs[0], nil
}

func (c *OvnNbClient) MutateLogicalRouterOps(ctx context.Context, lrName string, mutationFunc ...func(lr *ovnnb.LogicalRouter) *model.Mutation) ([]ovsdb.Operation, error) {
	logger := log.FromContext(ctx)

	if len(mutationFunc) == 0 {
		return nil, nil
	}

	lr, err := c.GetLogicalRouter(ctx, lrName, false)
	if err != nil {
		logger.Error(err, "failed to get logical router", "lr", lrName)
		return nil, err
	}

	mutations := make([]model.Mutation, 0, len(mutationFunc))
	for _, fn := range mutationFunc {
		mutation := fn(lr)
		if mutation != nil {
			mutations = append(mutations, *mutation)
		}
	}

	ops, err := c.ovsDbClient.Where(lr).Mutate(lr, mutations...)
	if err != nil {
		return nil, err
	}

	return ops, nil
}

func (c *OvnNbClient) UpdateLogicalRouterOps(ctx context.Context, lr *ovnnb.LogicalRouter, fields ...interface{}) ([]ovsdb.Operation, error) {
	if lr == nil {
		return nil, fmt.Errorf("logical router is nil")
	}
	return c.Where(lr).Update(lr, fields...)
}

//func (c *OvnNbClient) ClearLogicalRouterPolicy(ctx context.Context, lrName string) error {
//	logger := log.FromContext(ctx)
//
//	lr, err := c.GetLogicalRouter(ctx, lrName, false)
//	if err != nil {
//		logger.Error(err, "failed to get logical router when clear", "lr", lrName)
//		return err
//	}
//	lr.Policies = nil
//	ops, err := c.ovsDbClient.Where(lr).Update(lr, &lr.Policies)
//	if err != nil {
//		logger.Error(err, "failed to generate operations for clearing logical router policy", "lr", lrName)
//		return err
//	}
//
//	return c.Transact(ctx, "lr-policy-clear", ops)
//}
