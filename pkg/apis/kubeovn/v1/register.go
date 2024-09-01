package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// SchemeGroupVersion is group version used to register these objects
	SchemeGroupVersion = schema.GroupVersion{Group: "kubeovn.fengjinlin.io", Version: "v1"}

	//// SchemeBuilder initializes a scheme builder
	//SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}

	// AddToScheme is a global function that registers this API group & version to a scheme
	AddToScheme = SchemeBuilder.AddToScheme
)

//
//// Adds the list of known types to Scheme.
//func addKnownTypes(scheme *runtime.Scheme) error {
//	scheme.AddKnownTypes(SchemeGroupVersion,
//		&Vpc{}, &VpcList{},
//		&Subnet{}, &SubnetList{},
//	)
//	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
//	return nil
//}

// Kind takes an unqualified kind and returns back a Group qualified GroupKind
func Kind(kind string) schema.GroupKind {
	return SchemeGroupVersion.WithKind(kind).GroupKind()
}

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}
