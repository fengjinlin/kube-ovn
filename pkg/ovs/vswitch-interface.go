package ovs

import (
	"context"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	"k8s.io/klog/v2"
)

func (c *VSwitchClient) CleanDuplicateInterface(ifaceName, ifaceID string) error {
	duplicateIfaceFilter := func(iface *vswitch.Interface) bool {
		if iface.Name == ifaceName && len(iface.ExternalIDs) > 0 && iface.ExternalIDs[consts.ExternalIDsIfaceID] != ifaceID {
			return true
		}
		return false
	}
	ifaceList, err := c.ListInterfaceByFilter(duplicateIfaceFilter)
	if err != nil {
		return fmt.Errorf("list duplicate interfaces: %v", err)
	}
	if len(ifaceList) > 0 {
		var ops []ovsdb.Operation
		for _, iface := range ifaceList {
			delete(iface.ExternalIDs, consts.ExternalIDsIfaceID)
			if op, err := c.Where(iface).Update(iface, &iface.ExternalIDs); err != nil {
				return fmt.Errorf("generate operations for remove iface externalID %s: %v", consts.ExternalIDsIfaceID, err)
			} else {
				ops = append(ops, op...)
			}
		}
		if err = c.Transact(context.TODO(), "iface-update", ops); err != nil {
			return err
		}
	}

	return nil
}

func (c *VSwitchClient) ListInterfaceByFilter(filter func(iface *vswitch.Interface) bool) ([]*vswitch.Interface, error) {
	var ifaceList []*vswitch.Interface
	err := c.WhereCache(filter).List(context.TODO(), &ifaceList)
	return ifaceList, err
}

func (c *VSwitchClient) CreateInterfaceOp(portName string, iface *vswitch.Interface) ([]ovsdb.Operation, error) {
	if iface == nil {
		return nil, fmt.Errorf("interface is nil")
	}

	if iface.ExternalIDs == nil {
		iface.ExternalIDs = make(map[string]string)
	}

	ifaceCreateOp, err := c.Create(iface)
	if err != nil {
		klog.Error(err)
		return nil, fmt.Errorf("generate operations for creating interface %s: %v", iface.Name, err)
	}

	portUpdateMutation := func(port *vswitch.Port) *model.Mutation {
		return &model.Mutation{
			Field:   &port.Interfaces,
			Mutator: ovsdb.MutateOperationInsert,
			Value:   []string{iface.UUID},
		}
	}

	portUpdateOps, err := c.updatePortOp(portName, portUpdateMutation)
	if err != nil {
		klog.Error(err)
		return nil, fmt.Errorf("generate operations for adding interface %s: %v", iface.Name, err)
	}

	ops := make([]ovsdb.Operation, 0, len(ifaceCreateOp)+len(portUpdateOps))
	ops = append(ops, ifaceCreateOp...)
	ops = append(ops, portUpdateOps...)

	return ops, nil
}
