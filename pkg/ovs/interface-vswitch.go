package ovs

import (
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
	"github.com/ovn-org/libovsdb/ovsdb"
)

type VSwitchClientInterface interface {
	Bridge
	Port
	Interface
	OpenVSwitch
	Queue
	QoS

	Common
}

type Bridge interface {
	ListBridges(filter func(br *vswitch.Bridge) bool) ([]vswitch.Bridge, error)
	GetBridge(brName string, ignoreNotFound bool) (*vswitch.Bridge, error)
}

type Port interface {
	ListPorts(brName string) ([]vswitch.Port, error)
	CreatePort(brName, portName, ifaceName, ifaceType string, ifaceExternalIds map[string]string) error
	GetPort(portName string, ignoreNotFound bool) (*vswitch.Port, error)
	DeletePort(brName, portName string) error
	UpdatePortOps(port *vswitch.Port, fields ...interface{}) ([]ovsdb.Operation, error)
}

type Interface interface {
	CleanDuplicateInterface(ifaceID, ifaceName string) error
	ListInterfaceByFilter(filter func(iface *vswitch.Interface) bool) ([]*vswitch.Interface, error)
	UpdateInterface(iface *vswitch.Interface, fields ...interface{}) error
}

type OpenVSwitch interface {
	ListOpenVSwitch() ([]*vswitch.OpenvSwitch, error)
	UpdateOpenVSwitch(ovs *vswitch.OpenvSwitch, fields ...interface{}) error
}

type Queue interface {
	ListQueueByFilter(filter func(queue *vswitch.Queue) bool) ([]*vswitch.Queue, error)
	UpdateQueue(queue *vswitch.Queue, fields ...interface{}) error
	AddQueueOps(queue *vswitch.Queue) ([]ovsdb.Operation, error)
	GetQueueByUUID(uuid string) (*vswitch.Queue, error)
	DeleteQueueOps(queue *vswitch.Queue) ([]ovsdb.Operation, error)
}

type QoS interface {
	ListQosByFilter(filter func(qos *vswitch.QoS) bool) ([]*vswitch.QoS, error)
	AddQosOps(qos *vswitch.QoS) ([]ovsdb.Operation, error)
	DeleteQosOps(qos *vswitch.QoS) ([]ovsdb.Operation, error)
}
