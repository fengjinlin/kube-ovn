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
	PortTypeVethPair = "veth-pair"
)

const (
	GatewayCheckModeDisable = iota
	GatewayCheckModePing
	GatewayCheckModeArp
	GatewayCheckModePingNotConcerned
	GatewayCheckModeArpNotConcerned

	GatewayCheckMaxRetry = 200
)

const (
	PolicyRoutePriorityGateway = 29000
	PolicyRoutePriorityNode    = 30000
	PolicyRoutePrioritySubnet  = 31000
)

const (
	AclPriorityDefaultDrop     = 1000
	AclPriorityAllowSubnet     = 1001
	AclPriorityAllowJoinSubnet = 3000

	AclKeyParent = "parent"
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
	ExternalIDsKeyPodNS         = "pod-ns"
	ExternalIDsKeyPodNetNS      = "pod-net-ns"
	ExternalIDsKeySubnet        = "subnet"
	ExternalIDsKeyNode          = "node"
	ExternalIDsKeyKind          = "kind"

	ExternalIDsKeyIfaceID = "iface-id"
	ExternalIDsKeyIP      = "ip"

	ExternalIDsKeyServiceName = "svc-%s"
)

const (
	OtherConfigKeyMaxRate = "max-rate"
)

const (
	QosTypeHtb   = "linux-htb"
	QosTypeNetem = "linux-netem"
)
