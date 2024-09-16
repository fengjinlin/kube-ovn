package consts

const (
	AnnotationAllocated         = "ovn.kubernetes.io/allocated"
	AnnotationAllocatedTemplate = "%s.kubernetes.io/allocated"

	AnnotationRouted         = "ovn.kubernetes.io/routed"
	AnnotationRoutedTemplate = "%s.kubernetes.io/routed"

	AnnotationIPAddress          = "ovn.kubernetes.io/ip"
	AnnotationIPAddressTemplate  = "%s.kubernetes.io/ip"
	AnnotationMacAddress         = "ovn.kubernetes.io/mac"
	AnnotationMacAddressTemplate = "%s.kubernetes.io/mac"
	AnnotationCidr               = "ovn.kubernetes.io/cidr"
	AnnotationCidrTemplate       = "%s.kubernetes.io/cidr"
	AnnotationGateway            = "ovn.kubernetes.io/gateway"
	AnnotationGatewayTemplate    = "%s.kubernetes.io/gateway"

	AnnotationSubnet                   = "ovn.kubernetes.io/subnet"
	AnnotationSubnetTemplate           = "%s.kubernetes.io/subnet"
	AnnotationSubnetCidr               = "ovn.kubernetes.io/subnet_cidr"
	AnnotationSubnetCidrTemplate       = "%s.kubernetes.io/subnet_cidr"
	AnnotationSubnetExcludeIPs         = "ovn.kubernetes.io/subnet_exclude_ips"
	AnnotationSubnetExcludeIPsTemplate = "%s.kubernetes.io/subnet__exclude_ips"

	AnnotationVpc         = "ovn.kubernetes.io/vpc"
	AnnotationVpcTemplate = "%s.kubernetes.io/vpc"

	AnnotationPodNicTypeTemplate   = "%s.kubernetes.io/pod_nic_type"
	AnnotationDefaultRouteTemplate = "%s.kubernetes.io/default_route"

	AnnotationJoinIP      = "ovn.kubernetes.io/join_ip"
	AnnotationJoinMac     = "ovn.kubernetes.io/join_mac"
	AnnotationJoinCidr    = "ovn.kubernetes.io/join_cidr"
	AnnotationJoinGateway = "ovn.kubernetes.io/join_gateway"

	AnnotationJoinLogicalSwitch     = "ovn.kubernetes.io/join_ls"
	AnnotationJoinLogicalSwitchPort = "ovn.kubernetes.io/join_lsp"

	AnnotationVpcLastName     = "ovn.kubernetes.io/last_name"
	AnnotationVpcLastPolicies = "ovn.kubernetes.io/last_policies"

	AnnotationChassis = "ovn.kubernetes.io/chassis"

	AnnotationIngressRateTemplate = "%s.kubernetes.io/ingress_rate"
	AnnotationIngressRate         = "ovn.kubernetes.io/ingress_rate"
	AnnotationEgressRateTemplate  = "%s.kubernetes.io/egress_rate"
	AnnotationEgressRate          = "ovn.kubernetes.io/egress_rate"
)

const (
	LabelSubnetName = "ovn.fengjinlin.io/subnet"
	LabelNodeName   = "ovn.fengjinlin.io/node"
)

const (
	KindStatefulSet = "StatefulSet"
	KindNode        = "Node"

	FinalizerController        = "ovn.fengjinlin.io/controller"
	FinalizerDefaultProtection = "ovn.fengjinlin.io/default-protection"
)

const (
	HostnameEnv        = "KUBE_NODE_NAME"
	PodIP              = "POD_IP"
	ContentType        = "application/vnd.kubernetes.protobuf"
	AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
)
