// Code generated by "libovsdb.modelgen"
// DO NOT EDIT.

package ovnsb

const RBACRoleTable = "RBAC_Role"

// RBACRole defines an object in RBAC_Role table
type RBACRole struct {
	UUID        string            `ovsdb:"_uuid"`
	Name        string            `ovsdb:"name"`
	Permissions map[string]string `ovsdb:"permissions"`
}
