package v1

import (
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/klog/v2"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
//+kubebuilder:printcolumn:name="EnableExternal",type="boolean",JSONPath=".status.enableExternal"
//+kubebuilder:printcolumn:name="EnableBfd",type="boolean",JSONPath=".status.enableBfd"
//+kubebuilder:printcolumn:name="Subnets",type="string",JSONPath=".status.subnets"
//+kubebuilder:printcolumn:name="ExtraExternalSubnets",type="string",JSONPath=".status.extraExternalSubnets"
//+kubebuilder:printcolumn:name="Namespaces",type="string",JSONPath=".spec.namespaces"
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Vpc struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VpcSpec   `json:"spec"`
	Status VpcStatus `json:"status,omitempty"`
}

type VpcSpec struct {
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`
	// +optional
	StaticRoutes []*StaticRoute `json:"staticRoutes,omitempty"`
	// +optional
	PolicyRoutes []*PolicyRoute `json:"policyRoutes,omitempty"`
	// +optional
	VpcPeerings []*VpcPeering `json:"vpcPeerings,omitempty"`
	// +optional
	EnableExternal bool `json:"enableExternal,omitempty"`
	// +optional
	ExtraExternalSubnets []string `json:"extraExternalSubnets,omitempty"`
	// +optional
	EnableBfd bool `json:"enableBfd,omitempty"`
}

type RoutePolicy string

const (
	PolicySrc = RoutePolicy("policySrc")
	PolicyDst = RoutePolicy("policyDst")
)

type StaticRoute struct {
	Policy     RoutePolicy `json:"policy,omitempty"`
	CIDR       string      `json:"cidr"`
	NextHopIP  string      `json:"nextHopIP"`
	ECMPMode   string      `json:"ecmpMode"`
	BfdID      string      `json:"bfdId"`
	RouteTable string      `json:"routeTable"`
}

type PolicyRouteAction string

const (
	PolicyRouteActionAllow   = PolicyRouteAction("allow")
	PolicyRouteActionDrop    = PolicyRouteAction("drop")
	PolicyRouteActionReroute = PolicyRouteAction("reroute")
)

type PolicyRoute struct {
	Priority int               `json:"priority,omitempty"`
	Match    string            `json:"match,omitempty"`
	Action   PolicyRouteAction `json:"action,omitempty"`
	// NextHopIP is an optional parameter. It needs to be provided only when 'action' is 'reroute'.
	// +optional
	NextHopIP string `json:"nextHopIP,omitempty"`
}

type VpcPeering struct {
	RemoteVpc      string `json:"remoteVpc,omitempty"`
	LocalConnectIP string `json:"localConnectIP,omitempty"`
}

// VpcCondition describes the state of an object at a certain point.
// +k8s:deepcopy-gen=true
type VpcCondition Condition

type VpcStatus struct {
	// Conditions represents the latest state of the object
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []VpcCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// +optional
	Ready bool `json:"ready"`
	// +optional
	Default bool `json:"default"`
	// +optional
	DefaultSubnet string `json:"defaultSubnet"`
	// +optional
	Router string `json:"router"`
	// +optional
	TCPLoadBalancer string `json:"tcpLoadBalancer"`
	// +optional
	UDPLoadBalancer string `json:"udpLoadBalancer"`
	// +optional
	SctpLoadBalancer string `json:"sctpLoadBalancer"`
	// +optional
	TCPSessionLoadBalancer string `json:"tcpSessionLoadBalancer"`
	// +optional
	UDPSessionLoadBalancer string `json:"udpSessionLoadBalancer"`
	// +optional
	SctpSessionLoadBalancer string `json:"sctpSessionLoadBalancer"`
	// +optional
	Subnets []string `json:"subnets"`
	// +optional
	VpcPeerings []string `json:"vpcPeerings"`
	// +optional
	EnableExternal bool `json:"enableExternal"`
	// +optional
	ExtraExternalSubnets []string `json:"extraExternalSubnets"`
	// +optional
	EnableBfd bool `json:"enableBfd"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type VpcList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Vpc `json:"items"`
}

func (vs *VpcStatus) Bytes() ([]byte, error) {
	bytes, err := json.Marshal(vs)
	if err != nil {
		return nil, err
	}
	newStr := fmt.Sprintf(`{"status": %s}`, string(bytes))
	klog.V(5).Info("status body", newStr)
	return []byte(newStr), nil
}

func init() {
	SchemeBuilder.Register(&Vpc{}, &VpcList{})
}
