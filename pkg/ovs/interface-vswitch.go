package ovs

import (
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
)

type VSwitchClientInterface interface {
	Bridge
	Port
	Interface
	OpenVSwitch

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
}

type Interface interface {
	CleanDuplicateInterface(ifaceID, ifaceName string) error
	ListInterfaceByFilter(filter func(iface *vswitch.Interface) bool) ([]*vswitch.Interface, error)
}

type OpenVSwitch interface {
	ListOpenVSwitch() ([]*vswitch.OpenvSwitch, error)
	UpdateOpenVSwitch(ovs *vswitch.OpenvSwitch, fields ...interface{}) error
}
