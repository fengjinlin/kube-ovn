package schema

//go:generate modelgen -p vswitch -o ../vswitch/ vswitch.ovsschema

//go:generate modelgen -p ovnnb -o ../ovnnb/ ovn-nb.ovsschema

//go:generate modelgen -p ovnsb -o ../ovnsb/ ovn-sb.ovsschema
