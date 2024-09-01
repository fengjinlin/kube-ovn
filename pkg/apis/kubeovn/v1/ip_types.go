package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="V4IP",type="string",JSONPath=".spec.v4IpAddress"
// +kubebuilder:printcolumn:name="V6IP",type="string",JSONPath=".spec.v6IpAddress"
// +kubebuilder:printcolumn:name="Mac",type="string",JSONPath=".spec.macAddress"
// +kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="Subnet",type="string",JSONPath=".spec.subnet"
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced

type IP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec IPSpec `json:"spec"`
}

type IPSpec struct {
	PodName string `json:"podName"`
	// +optional
	Namespace string `json:"namespace"`
	Subnet    string `json:"subnet"`
	// +optional
	AttachSubnets []string `json:"attachSubnets"`
	NodeName      string   `json:"nodeName"`
	IPAddress     string   `json:"ipAddress"`
	// +optional
	V4IPAddress string `json:"v4IpAddress"`
	// +optional
	V6IPAddress string `json:"v6IpAddress"`
	// +optional
	AttachIPs  []string `json:"attachIps"`
	MacAddress string   `json:"macAddress"`
	// +optional
	AttachMacs []string `json:"attachMacs"`
	// +optional
	ContainerID string `json:"containerID"`
	// +optional
	PodType string `json:"podType"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type IPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []IP `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IP{}, &IPList{})
}
