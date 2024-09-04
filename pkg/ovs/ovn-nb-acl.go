package ovs

import (
	"context"
	"fmt"

	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"

	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
)

func (c *OvnNbClient) GetACLByUUID(ctx context.Context, uuid string) (*ovnnb.ACL, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	acl := &ovnnb.ACL{
		UUID: uuid,
	}

	if err := c.Get(ctx, acl); err != nil {
		return nil, err
	}

	return acl, nil
}

func (c *OvnNbClient) DeleteSwitchACL(ctx context.Context, lsName, uuid string) error {
	mutationFunc := func(ls *ovnnb.LogicalSwitch) *model.Mutation {
		return &model.Mutation{
			Field:   &ls.ACLs,
			Mutator: ovsdb.MutateOperationDelete,
			Value:   []string{uuid},
		}
	}

	ops, err := c.MutateLogicalSwitchOp(ctx, lsName, mutationFunc)
	if err != nil {
		return fmt.Errorf("generate operations for deleting acl from logical switch: %v", err)
	}

	if err = c.Transact(ctx, "ls-acl-del", ops); err != nil {
		return err
	}
	return nil
}

func (c *OvnNbClient) CreateSwitchACL(ctx context.Context, lsName string, acl *ovnnb.ACL) error {
	aclCreateOps, err := c.Create(acl)
	if err != nil {
		return fmt.Errorf("generate operations for creating acl: %v", err)
	}

	mutationFunc := func(ls *ovnnb.LogicalSwitch) *model.Mutation {
		return &model.Mutation{
			Field:   &ls.ACLs,
			Mutator: ovsdb.MutateOperationInsert,
			Value:   []string{acl.UUID},
		}
	}
	lsMutateOps, err := c.MutateLogicalSwitchOp(ctx, lsName, mutationFunc)
	if err != nil {
		return fmt.Errorf("generate operations for adding acl to logical switch: %v", err)
	}

	ops := make([]ovsdb.Operation, 0, len(aclCreateOps)+len(lsMutateOps))
	ops = append(ops, aclCreateOps...)
	ops = append(ops, lsMutateOps...)

	if err = c.Transact(ctx, "ls-acl-add", ops); err != nil {
		return err
	}

	return nil
}
