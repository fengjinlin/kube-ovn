package ovs

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"

	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
)

func (c *OvnNbClient) GetLoadBalancer(ctx context.Context, lbName string, ignoreNotFound bool) (*ovnnb.LoadBalancer, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	var (
		lbList []ovnnb.LoadBalancer
		err    error
	)

	lbList = make([]ovnnb.LoadBalancer, 0)
	if err = c.WhereCache(
		func(lb *ovnnb.LoadBalancer) bool {
			return lb.Name == lbName
		},
	).List(ctx, &lbList); err != nil {
		return nil, fmt.Errorf("failed to list load balancer %q: %v", lbName, err)
	}

	switch {
	// not found
	case len(lbList) == 0:
		if ignoreNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("not found load balancer %q", lbName)
	case len(lbList) > 1:
		return nil, fmt.Errorf("more than one load balancer with same name %q", lbName)
	default:
		return &lbList[0], nil
	}
}

func (c *OvnNbClient) CreateLoadBalancer(ctx context.Context, lb *ovnnb.LoadBalancer) error {
	var (
		err error
		ops []ovsdb.Operation
	)
	lb.UUID = c.NamedUUID()
	if ops, err = c.Create(lb); err != nil {
		return fmt.Errorf("generate operations for creating load balancer %s: %v", lb.Name, err)
	}
	if err = c.Transact(ctx, "lb-add", ops); err != nil {
		return fmt.Errorf("create load balancer %s: %v", lb.Name, err)
	}
	return nil
}

func (c *OvnNbClient) UpdateLoadBalancer(ctx context.Context, lb *ovnnb.LoadBalancer, fields ...interface{}) error {
	var (
		err error
		ops []ovsdb.Operation
	)
	if ops, err = c.Where(lb).Update(lb, fields...); err != nil {
		return fmt.Errorf("generate operations for updating load balancer %s: %v", lb.Name, err)
	}

	if err = c.Transact(ctx, "lb-update", ops); err != nil {
		return fmt.Errorf("update load balancer %s: %v", lb.Name, err)
	}

	return nil
}

func (c *OvnNbClient) DeleteLoadBalancer(ctx context.Context, lbName string) error {
	var (
		err error
		lb  *ovnnb.LoadBalancer
		ops []ovsdb.Operation
	)
	if lb, err = c.GetLoadBalancer(ctx, lbName, true); err != nil {
		return fmt.Errorf("get load balancer %s: %v", lbName, err)
	}
	if lb == nil {
		return nil
	}
	if ops, err = c.Where(lb).Delete(); err != nil {
		return fmt.Errorf("generate operations for deleting load balancer %s: %v", lbName, err)
	}
	if err = c.Transact(ctx, "lb-del", ops); err != nil {
		return fmt.Errorf("delete load balancer %s: %v", lbName, err)
	}

	return nil
}

func (c *OvnNbClient) MutateLoadBalancerOp(ctx context.Context, lbName string, mutationFunc ...func(ls *ovnnb.LoadBalancer) []*model.Mutation) ([]ovsdb.Operation, error) {
	if len(mutationFunc) == 0 {
		return nil, nil
	}

	lb, err := c.GetLoadBalancer(ctx, lbName, false)
	if err != nil {
		return nil, fmt.Errorf("get load balancer %s: %v", lbName, err)
	}

	mutations := make([]model.Mutation, 0, len(mutationFunc))
	for _, fn := range mutationFunc {
		mutation := fn(lb)
		for _, m := range mutation {
			mutations = append(mutations, *m)
		}
	}

	if len(mutations) > 0 {
		ops, err := c.ovsDbClient.Where(lb).Mutate(lb, mutations...)
		if err != nil {
			return nil, fmt.Errorf("generate operations for mutating load balancer %s: %v", lbName, err)
		}

		return ops, nil
	}

	return nil, nil
}

func (c *OvnNbClient) AddLoadBalancerVip(ctx context.Context, lbName, vip string, backends ...string) error {
	var (
		err error
		ops []ovsdb.Operation
	)
	sort.Strings(backends)
	mutationFunc := func(lb *ovnnb.LoadBalancer) []*model.Mutation {
		var (
			mutations = make([]*model.Mutation, 0, 2)
			temp      = strings.Join(backends, ",")
		)
		if len(lb.Vips) > 0 {
			if lb.Vips[vip] == temp {
				return nil
			} else {
				mutations = append(mutations, &model.Mutation{
					Field:   &lb.Vips,
					Mutator: ovsdb.MutateOperationDelete,
					Value:   map[string]string{vip: lb.Vips[vip]},
				})
			}
		}
		mutations = append(mutations, &model.Mutation{
			Field:   &lb.Vips,
			Mutator: ovsdb.MutateOperationInsert,
			Value:   map[string]string{vip: temp},
		})
		return mutations
	}

	if ops, err = c.MutateLoadBalancerOp(ctx, lbName, mutationFunc); err != nil {
		return fmt.Errorf("generate operations for adding vip `%s` to load balancer `%s`: %v", vip, lbName, err)
	}

	if len(ops) == 0 {
		return nil
	}

	if err = c.Transact(ctx, "lb-vip-add", ops); err != nil {
		return err
	}

	return nil
}

func (c *OvnNbClient) DeleteLoadBalancerVip(ctx context.Context, lbName, vip string) error {
	var (
		err error
		ops []ovsdb.Operation
	)
	mutationFunc := func(lb *ovnnb.LoadBalancer) []*model.Mutation {
		return []*model.Mutation{
			{
				Field:   &lb.Vips,
				Mutator: ovsdb.MutateOperationDelete,
				Value:   map[string]string{vip: lb.Vips[vip]},
			},
		}
	}
	if ops, err = c.MutateLoadBalancerOp(ctx, lbName, mutationFunc); err != nil {
		return fmt.Errorf("generate operations for deleting vip `%s` from load balancer `%s`: %v", vip, lbName, err)
	}
	if err = c.Transact(ctx, "lb-vip-del", ops); err != nil {
		return err
	}
	return nil
}
