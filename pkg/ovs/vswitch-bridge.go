package ovs

import (
	"context"
	"fmt"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"

	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
)

func (c *VSwitchClient) ListBridges(filter func(br *vswitch.Bridge) bool) ([]vswitch.Bridge, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	results := make([]vswitch.Bridge, 0)

	if err := c.WhereCache(filter).List(ctx, &results); err != nil {
		return nil, fmt.Errorf("list bridge: %v", err)
	}

	return results, nil
}

func (c *VSwitchClient) GetBridge(brName string, ignoreNotFound bool) (*vswitch.Bridge, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	brs := make([]vswitch.Bridge, 0)
	if err := c.ovsDbClient.WhereCache(func(br *vswitch.Bridge) bool { return br.Name == brName }).List(ctx, &brs); err != nil {
		return nil, fmt.Errorf("list vswitch bridge %s: %v", brName, err)
	}
	if len(brs) == 0 {
		if ignoreNotFound {
			return nil, nil
		} else {
			return nil, fmt.Errorf("no vswitch bridge found with name %q", brName)
		}
	}

	if len(brs) > 1 {
		return nil, fmt.Errorf("found more than one vswitch bridge with name %q", brName)
	}

	return &brs[0], nil
}

func (c *VSwitchClient) bridgeUpdateOp(brName string, mutationFunc ...func(ls *vswitch.Bridge) *model.Mutation) ([]ovsdb.Operation, error) {
	if len(mutationFunc) == 0 {
		return nil, nil
	}

	br, err := c.GetBridge(brName, false)
	if err != nil {
		return nil, fmt.Errorf("get bridge %s: %v", brName, err)
	}

	mutations := make([]model.Mutation, 0, len(mutationFunc))
	for _, fn := range mutationFunc {
		mutation := fn(br)
		if mutation != nil {
			mutations = append(mutations, *mutation)
		}
	}

	ops, err := c.ovsDbClient.Where(br).Mutate(br, mutations...)
	if err != nil {
		return nil, fmt.Errorf("generate operations for mutating bridge %s: %v", brName, err)
	}

	return ops, nil
}
