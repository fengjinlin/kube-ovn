package ovs

import (
	"context"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
	"github.com/ovn-org/libovsdb/ovsdb"
)

func (c *VSwitchClient) ListQosByFilter(filter func(qos *vswitch.QoS) bool) ([]*vswitch.QoS, error) {
	var qosList []*vswitch.QoS
	err := c.WhereCache(filter).List(context.TODO(), &qosList)
	return qosList, err
}

func (c *VSwitchClient) AddQosOps(qos *vswitch.QoS) ([]ovsdb.Operation, error) {
	return c.Create(qos)
}

func (c *VSwitchClient) DeleteQosOps(qos *vswitch.QoS) ([]ovsdb.Operation, error) {
	return c.Where(qos).Delete()
}
