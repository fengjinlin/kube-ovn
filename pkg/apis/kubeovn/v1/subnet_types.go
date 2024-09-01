package v1

import (
	"encoding/json"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Provider",type="string",JSONPath=".spec.provider"
// +kubebuilder:printcolumn:name="Vpc",type="string",JSONPath=".spec.vpc"
// +kubebuilder:printcolumn:name="Protocol",type="string",JSONPath=".spec.protocol"
// +kubebuilder:printcolumn:name="CIDR",type="string",JSONPath=".spec.cidrBlock"
// +kubebuilder:printcolumn:name="Private",type="string",JSONPath=".spec.private"
// +kubebuilder:printcolumn:name="Default",type="string",JSONPath=".spec.default"
// +kubebuilder:printcolumn:name="GatewayType",type="string",JSONPath=".spec.gatewayType"
// +kubebuilder:printcolumn:name="V4Used",type="string",JSONPath=".status.v4UsingIPs"
// +kubebuilder:printcolumn:name="V4Available",type="string",JSONPath=".status.v4AvailableIPs"
// +kubebuilder:printcolumn:name="V6Used",type="string",JSONPath=".status.v6UsingIPs"
// +kubebuilder:printcolumn:name="V6Available",type="string",JSONPath=".status.v6AvailableIPs"
// +kubebuilder:printcolumn:name="ExcludeIPs",type="string",JSONPath=".spec.excludeIps"
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced

type Subnet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubnetSpec   `json:"spec"`
	Status SubnetStatus `json:"status,omitempty"`
}

type SubnetSpec struct {
	// +kubebuilder:default=false
	Default bool   `json:"default"`
	Vpc     string `json:"vpc,omitempty"`
	// +kubebuilder:validation:Enum=IPv4;IPv6;Dual
	// +optional
	Protocol string `json:"protocol,omitempty"`
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`
	CIDRBlock  string   `json:"cidrBlock"`
	Gateway    string   `json:"gateway"`
	// +optional
	ExcludeIPs []string `json:"excludeIps,omitempty"`
	// +optional
	Provider string `json:"provider,omitempty"`
	// +kubebuilder:validation:Enum=distributed;centralized
	// +kubebuilder:default=distributed
	GatewayType string `json:"gatewayType,omitempty"`
	// +optional
	GatewayNode string `json:"gatewayNode"`
	// +kubebuilder:default=false
	NatOutgoing bool `json:"natOutgoing"`
	// +optional
	ExternalEgressGateway string `json:"externalEgressGateway,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=32765
	// +optional
	PolicyRoutingPriority uint32 `json:"policyRoutingPriority,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	// +optional
	PolicyRoutingTableID uint32 `json:"policyRoutingTableID,omitempty"`

	// +kubebuilder:validation:Minimum=68
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Mtu uint32 `json:"mtu,omitempty"`

	// +optional
	Vlan string `json:"vlan,omitempty"`
	// +optional
	Vips []string `json:"vips,omitempty"`

	// +optional
	LogicalGateway bool `json:"logicalGateway,omitempty"`
	// +optional
	DisableGatewayCheck bool `json:"disableGatewayCheck,omitempty"`
	// +optional
	DisableInterConnection bool `json:"disableInterConnection,omitempty"`

	// +optional
	EnableDHCP bool `json:"enableDHCP,omitempty"`
	// +optional
	DHCPv4Options string `json:"dhcpV4Options,omitempty"`
	// +optional
	DHCPv6Options string `json:"dhcpV6Options,omitempty"`

	// +optional
	EnableIPv6RA bool `json:"enableIPv6RA,omitempty"`
	// +optional
	IPv6RAConfigs string `json:"ipv6RAConfigs,omitempty"`

	//Acls []ACL `json:"acls,omitempty"`

	//NatOutgoingPolicyRules []NatOutgoingPolicyRule `json:"natOutgoingPolicyRules,omitempty"`

	// +optional
	U2OInterconnectionIP string `json:"u2oInterconnectionIP,omitempty"`
	// +optional
	U2OInterconnection bool `json:"u2oInterconnection,omitempty"`
	// +optional
	EnableLb *bool `json:"enableLb,omitempty"`
	// +optional
	EnableEcmp bool `json:"enableEcmp,omitempty"`

	// +optional
	RouteTable string `json:"routeTable,omitempty"`
}

// SubnetCondition describes the state of an object at a certain point.
// +k8s:deepcopy-gen=true
type SubnetCondition Condition

type SubnetStatus struct {
	// Conditions represents the latest state of the object
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []SubnetCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// +optional
	V4AvailableIPs float64 `json:"v4AvailableIPs"`
	// +optional
	V4AvailableIPRange string `json:"v4AvailableIPRange"`
	// +optional
	V4UsingIPs float64 `json:"v4UsingIPs"`
	// +optional
	V4UsingIPRange string `json:"v4UsingIPRange"`
	// +optional
	V6AvailableIPs float64 `json:"v6AvailableIPs"`
	// +optional
	V6AvailableIPRange string `json:"v6AvailableIPRange"`
	// +optional
	V6UsingIPs float64 `json:"v6UsingIPs"`
	// +optional
	V6UsingIPRange string `json:"V6UsingIPRange"`
	// +optional
	ActivateGateway string `json:"activateGateway"`
	// +optional
	DHCPv4OptionsUUID string `json:"dhcpV4OptionsUUID"`
	// +optional
	DHCPv6OptionsUUID string `json:"dhcpV6OptionsUUID"`
	// +optional
	U2OInterconnectionIP string `json:"u2oInterconnectionIP"`
	// +optional
	U2OInterconnectionVPC string `json:"u2oInterconnectionVPC"`
	//NatOutgoingPolicyRules []NatOutgoingPolicyRuleStatus `json:"natOutgoingPolicyRules"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type SubnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Subnet `json:"items"`
}

func (ss *SubnetStatus) Bytes() ([]byte, error) {
	// {"availableIPs":65527,"usingIPs":9} => {"status": {"availableIPs":65527,"usingIPs":9}}
	bytes, err := json.Marshal(ss)
	if err != nil {
		return nil, err
	}
	newStr := fmt.Sprintf(`{"status": %s}`, string(bytes))
	klog.V(5).Info("status body", newStr)
	return []byte(newStr), nil
}

func init() {
	SchemeBuilder.Register(&Subnet{}, &SubnetList{})
}
