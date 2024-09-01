package ovs

import (
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	"k8s.io/klog/v2"
)

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

	portUpdateOps, err := c.portUpdateOp(portName, portUpdateMutation)
	if err != nil {
		klog.Error(err)
		return nil, fmt.Errorf("generate operations for adding interface %s: %v", iface.Name, err)
	}

	ops := make([]ovsdb.Operation, 0, len(ifaceCreateOp)+len(portUpdateOps))
	ops = append(ops, ifaceCreateOp...)
	ops = append(ops, portUpdateOps...)

	return ops, nil
}
