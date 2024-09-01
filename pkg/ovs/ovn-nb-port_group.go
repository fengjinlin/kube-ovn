package ovs

import (
	"context"
	"errors"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
)

func (c *OvnNbClient) DeletePortGroup(ctx context.Context, pgName string) error {
	pg, err := c.GetPortGroup(ctx, pgName, true)
	if err != nil {
		return err
	}

	if pg == nil {
		return nil
	}

	ops, err := c.ovsDbClient.Where(pg).Delete()
	if err != nil {
		return err
	}

	if err = c.Transact(ctx, "pg-del", ops); err != nil {
		return err
	}

	return nil
}

func (c *OvnNbClient) CreatePortGroup(ctx context.Context, pgName string, externalIDs map[string]string) error {
	pg, err := c.GetPortGroup(ctx, pgName, true)
	if err != nil {
		return err
	}
	if pg != nil {
		return nil
	}
	pg = &ovnnb.PortGroup{
		ExternalIDs: externalIDs,
		Name:        pgName,
	}
	ops, err := c.Create(pg)
	if err != nil {
		return fmt.Errorf("generate operations for creating port group %s: %v", pgName, err)
	}
	if err = c.Transact(ctx, "pg-add", ops); err != nil {
		return err
	}
	return nil
}

func (c *OvnNbClient) GetPortGroup(ctx context.Context, pgName string, ignoreNotFound bool) (*ovnnb.PortGroup, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	pg := &ovnnb.PortGroup{Name: pgName}
	if err := c.Get(ctx, pg); err != nil {
		if ignoreNotFound && errors.Is(err, client.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get port group %s: %v", pgName, err)
	}

	return pg, nil
}

func (c *OvnNbClient) MutatePortGroupOp(ctx context.Context, pgName string, mutationFunc ...func(ls *ovnnb.PortGroup) *model.Mutation) ([]ovsdb.Operation, error) {
	if len(mutationFunc) == 0 {
		return nil, nil
	}

	pg, err := c.GetPortGroup(ctx, pgName, false)
	if err != nil {
		return nil, fmt.Errorf("get port group %s: %v", pgName, err)
	}

	mutations := make([]model.Mutation, 0, len(mutationFunc))
	for _, fn := range mutationFunc {
		mutation := fn(pg)
		if mutation != nil {
			mutations = append(mutations, *mutation)
		}
	}

	ops, err := c.ovsDbClient.Where(pg).Mutate(pg, mutations...)
	if err != nil {
		return nil, fmt.Errorf("generate operations for mutating port group %s: %v", pgName, err)
	}

	return ops, nil
}

func (c *OvnNbClient) AddPortGroupPorts(ctx context.Context, pgName string, lspNames ...string) error {
	ops, err := c.AddPortGroupPortsOp(ctx, pgName, lspNames...)
	if err != nil {
		return err
	}
	if err = c.Transact(ctx, "pg-ports-add", ops); err != nil {
		return fmt.Errorf("port group %s add ports %v: %v", pgName, lspNames, err)
	}

	return nil
}

func (c *OvnNbClient) AddPortGroupPortsOp(ctx context.Context, pgName string, lspNames ...string) ([]ovsdb.Operation, error) {
	if pgName == "" {
		return nil, fmt.Errorf("port group name is empty")
	}

	var lspUUIDs []string
	for _, lspName := range lspNames {
		lsp, err := c.GetLogicalSwitchPort(ctx, lspName, false)

		if err != nil {
			return nil, err
		}
		lspUUIDs = append(lspUUIDs, lsp.UUID)
	}

	if len(lspUUIDs) == 0 {
		return nil, fmt.Errorf("no logical switch ports found")
	}

	mutation := func(pg *ovnnb.PortGroup) *model.Mutation {
		return &model.Mutation{
			Field:   &pg.Ports,
			Mutator: ovsdb.MutateOperationInsert,
			Value:   lspUUIDs,
		}
	}

	return c.MutatePortGroupOp(ctx, pgName, mutation)
}
