// Code generated by "libovsdb.modelgen"
// DO NOT EDIT.

package vswitch

const PortTable = "Port"

type (
	PortBondMode = string
	PortLACP     = string
	PortVLANMode = string
)

var (
	PortBondModeBalanceTCP     PortBondMode = "balance-tcp"
	PortBondModeBalanceSLB     PortBondMode = "balance-slb"
	PortBondModeActiveBackup   PortBondMode = "active-backup"
	PortLACPActive             PortLACP     = "active"
	PortLACPPassive            PortLACP     = "passive"
	PortLACPOff                PortLACP     = "off"
	PortVLANModeTrunk          PortVLANMode = "trunk"
	PortVLANModeAccess         PortVLANMode = "access"
	PortVLANModeNativeTagged   PortVLANMode = "native-tagged"
	PortVLANModeNativeUntagged PortVLANMode = "native-untagged"
	PortVLANModeDot1qTunnel    PortVLANMode = "dot1q-tunnel"
)

// Port defines an object in Port table
type Port struct {
	UUID            string            `ovsdb:"_uuid"`
	BondActiveSlave *string           `ovsdb:"bond_active_slave"`
	BondDowndelay   int               `ovsdb:"bond_downdelay"`
	BondFakeIface   bool              `ovsdb:"bond_fake_iface"`
	BondMode        *PortBondMode     `ovsdb:"bond_mode"`
	BondUpdelay     int               `ovsdb:"bond_updelay"`
	CVLANs          []int             `ovsdb:"cvlans"`
	ExternalIDs     map[string]string `ovsdb:"external_ids"`
	FakeBridge      bool              `ovsdb:"fake_bridge"`
	Interfaces      []string          `ovsdb:"interfaces"`
	LACP            *PortLACP         `ovsdb:"lacp"`
	MAC             *string           `ovsdb:"mac"`
	Name            string            `ovsdb:"name"`
	OtherConfig     map[string]string `ovsdb:"other_config"`
	Protected       bool              `ovsdb:"protected"`
	QOS             *string           `ovsdb:"qos"`
	RSTPStatistics  map[string]int    `ovsdb:"rstp_statistics"`
	RSTPStatus      map[string]string `ovsdb:"rstp_status"`
	Statistics      map[string]int    `ovsdb:"statistics"`
	Status          map[string]string `ovsdb:"status"`
	Tag             *int              `ovsdb:"tag"`
	Trunks          []int             `ovsdb:"trunks"`
	VLANMode        *PortVLANMode     `ovsdb:"vlan_mode"`
}
