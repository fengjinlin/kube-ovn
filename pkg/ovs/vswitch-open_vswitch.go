package ovs

import (
	"context"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
	"github.com/ovn-org/libovsdb/ovsdb"
)

func (c *VSwitchClient) ListOpenVSwitch() ([]*vswitch.OpenvSwitch, error) {
	ctx, cancel := context.WithTimeout(context.TODO(), c.Timeout)
	defer cancel()

	results := make([]*vswitch.OpenvSwitch, 0, 1)

	if err := c.List(ctx, &results); err != nil {
		return nil, fmt.Errorf("list bridge: %v", err)
	}

	return results, nil
}

func (c *VSwitchClient) UpdateOpenVSwitch(ovs *vswitch.OpenvSwitch, fields ...interface{}) error {
	var (
		err error
		ops []ovsdb.Operation
	)
	if ops, err = c.Where(ovs).Update(ovs, fields...); err != nil {
		return fmt.Errorf("generate operations for updating openvswitch %v", err)
	}

	if err = c.Transact(context.TODO(), "ovs-update", ops); err != nil {
		return fmt.Errorf("update openvswitch: %v", err)
	}

	return nil
}
