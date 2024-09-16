package ovs

import (
	"context"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
)

func (c *VSwitchClient) ListPorts(brName string) ([]vswitch.Port, error) {
	br, err := c.GetBridge(brName, false)
	if err != nil {
		return nil, err
	}

	ports := make([]vswitch.Port, 0)
	for _, portUUID := range br.Ports {
		port, err := c.getPortByUUID(portUUID)
		if err != nil {
			return nil, err
		}
		ports = append(ports, *port)
	}

	return ports, nil
}

func (c *VSwitchClient) getPortByUUID(uuid string) (*vswitch.Port, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	port := &vswitch.Port{
		UUID: uuid,
	}
	err := c.Get(ctx, port)
	if err != nil {
		return nil, err
	}

	return port, nil
}

func (c *VSwitchClient) CreatePort(brName, portName, ifaceName, ifaceType string, ifaceExternalIds map[string]string) error {
	ops, err := c.CreatePortOp(brName, portName, ifaceName, ifaceType, ifaceExternalIds)
	if err != nil {
		return err
	}

	if err := c.Transact(context.Background(), "port-add", ops); err != nil {
		return fmt.Errorf("create port %s: %v", portName, err)
	}

	return nil
}

func (c *VSwitchClient) CreatePortOp(brName, portName, ifaceName, ifaceType string, ifaceExternalIds map[string]string) ([]ovsdb.Operation, error) {
	iface := &vswitch.Interface{
		UUID:        c.NamedUUID(),
		ExternalIDs: ifaceExternalIds,
		Name:        ifaceName,
		Type:        ifaceType,
	}
	ifaceCreateOps, err := c.Create(iface)
	if err != nil {
		return nil, fmt.Errorf("generate operations for creating interface %q: %v", ifaceName, err)
	}

	port := &vswitch.Port{
		UUID:       c.NamedUUID(),
		Interfaces: []string{iface.UUID},
		Name:       portName,
	}
	portCreateOps, err := c.Create(port)
	if err != nil {
		return nil, fmt.Errorf("generate operations for creating port %q: %v", portName, err)
	}

	brUpdateMutation := func(br *vswitch.Bridge) *model.Mutation {
		return &model.Mutation{
			Field:   &br.Ports,
			Mutator: ovsdb.MutateOperationInsert,
			Value:   []string{port.UUID},
		}
	}
	brUpdateOps, err := c.updateBridgeOp(brName, brUpdateMutation)
	if err != nil {
		return nil, fmt.Errorf("generate operations for updating bridge %q: %v", brName, err)
	}

	ops := make([]ovsdb.Operation, 0, len(ifaceCreateOps)+len(portCreateOps)+len(brUpdateOps))
	ops = append(ops, ifaceCreateOps...)
	ops = append(ops, portCreateOps...)
	ops = append(ops, brUpdateOps...)

	return ops, nil
}

func (c *VSwitchClient) GetPort(portName string, ignoreNotFound bool) (*vswitch.Port, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	portList := make([]vswitch.Port, 0)
	if err := c.ovsDbClient.WhereCache(func(port *vswitch.Port) bool { return port.Name == portName }).List(ctx, &portList); err != nil {
		return nil, fmt.Errorf("list port %q: %v", portName, err)
	}

	if len(portList) == 0 {
		if ignoreNotFound {
			return nil, nil
		} else {
			return nil, fmt.Errorf("not found port %q", portName)
		}
	}

	if len(portList) > 1 {
		return nil, fmt.Errorf("more than one port with same name %q", portName)
	}

	return &portList[0], nil
}

func (c *VSwitchClient) updatePortOp(portName string, mutationFunc ...func(ls *vswitch.Port) *model.Mutation) ([]ovsdb.Operation, error) {
	if len(mutationFunc) == 0 {
		return nil, nil
	}

	port, err := c.GetPort(portName, false)
	if err != nil {
		return nil, fmt.Errorf("get port %s: %v", portName, err)
	}

	mutations := make([]model.Mutation, 0, len(mutationFunc))
	for _, fn := range mutationFunc {
		mutation := fn(port)
		if mutation != nil {
			mutations = append(mutations, *mutation)
		}
	}

	ops, err := c.ovsDbClient.Where(port).Mutate(port, mutations...)
	if err != nil {
		return nil, fmt.Errorf("generate operations for mutating port %s: %v", portName, err)
	}

	return ops, nil
}

func (c *VSwitchClient) DeletePort(brName, portName string) error {
	port, err := c.GetPort(portName, false)
	if err != nil {
		return err
	}
	brUpdateMutation := func(br *vswitch.Bridge) *model.Mutation {
		return &model.Mutation{
			Field:   &br.Ports,
			Mutator: ovsdb.MutateOperationDelete,
			Value:   []string{port.UUID},
		}
	}
	ops, err := c.updateBridgeOp(brName, brUpdateMutation)
	if err != nil {
		return err
	}
	if err := c.Transact(context.Background(), "port-del", ops); err != nil {
		return fmt.Errorf("failed to del port %s: %v", portName, err)
	}

	return nil
}

func (c *VSwitchClient) UpdatePortOps(port *vswitch.Port, fields ...interface{}) ([]ovsdb.Operation, error) {
	return c.Where(port).Update(port, fields...)
}
