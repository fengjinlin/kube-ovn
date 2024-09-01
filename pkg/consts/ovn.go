package consts

const (
	NetworkTypeVlan   = "vlan"
	NetworkTypeGeneve = "geneve"
	NetworkTypeVxlan  = "vxlan"
	NetworkTypeStt    = "stt"

	GeneveHeaderLength = 100
	VxlanHeaderLength  = 50
	SttHeaderLength    = 72
	TCPIPHeaderLength  = 40

	GatewayTypeDistributed = "distributed"
	GatewayTypeCentralized = "centralized"

	NormalRouteType    = "normal"
	EcmpRouteType      = "ecmp"
	StaticRouteBfdEcmp = "ecmp_symmetric_reply"

	RouteTableMain = ""

	DefaultBridgeName = "br-int"

	NodeNicName = "ovn0"

	IfaceTypeDefault  = ""
	IfaceTypeInternal = "internal"
	IfaceTypeGeneve   = "geneve"

	PortTypeOffload  = "offload-port"
	PortTypeInternal = "internal-port"
	PortTypeDpdk     = "dpdk-port"
)

const (
	PolicyRoutePriorityGateway = 29000
	PolicyRoutePriorityNode    = 30000
	PolicyRoutePrioritySubnet  = 31000
)

const (
	InterconnectionSwitch = "ts"

	DefaultServiceSessionAffinityTimeout = 10800

	ProtocolTcp  = "tcp"
	ProtocolUdp  = "udp"
	ProtocolSctp = "sctp"
)

const (
	ExternalIDsKeyVendor        = "vendor"
	ExternalIDsKeyLogicalRouter = "lr"
	ExternalIDsKeyLogicalSwitch = "ls"
	ExternalIDsKeyNetworkPolicy = "np"
	ExternalIDsKeyPod           = "pod"
	ExternalIDsKeySubnet        = "subnet"
	ExternalIDsKeyNode          = "node"
	ExternalIDsKeyKind          = "kind"

	ExternalIDsKeyServiceName = "svc-%s"
)
